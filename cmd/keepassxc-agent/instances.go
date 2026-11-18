package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
	"unicode"

	"keepassxc-agent-middleware/internal/install"
	"keepassxc-agent-middleware/internal/instances"
	"keepassxc-agent-middleware/internal/settings"
	"keepassxc-agent-middleware/internal/transport"
)

type instanceView struct {
	PID     int    `json:"pid"`
	Default bool   `json:"default"`
	Listen  string `json:"listen"`
	Binary  string `json:"binary"`
	Config  string `json:"config,omitempty"`
	LogFile string `json:"log_file,omitempty"`
	Started string `json:"started,omitempty"`
}

func runInstances(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("instances", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "output running instances as JSON")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: keepassxc-agent instances [--json]")
	}
	defaultEndpoint := transport.DefaultListen()
	binary, _ := os.Executable()
	fallback := &instances.Candidate{Endpoint: defaultEndpoint, Binary: binary}
	config, err := settings.Load(settings.DefaultPath())
	if err == nil {
		if config.Listen != "" {
			defaultEndpoint = config.Listen
			fallback.Endpoint = config.Listen
		}
		fallback.Config = settings.DefaultPath()
		fallback.LogFile = config.LogFile
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "Cannot read installed configuration: %v\n", err)
	}
	if manifest, err := install.ReadManifest(settings.Root() + string(os.PathSeparator) + "install.json"); err == nil && manifest.Version == 1 && manifest.OS == runtime.GOOS && !manifest.Uninstalling {
		fallback.Binary = manifest.Binary
	}
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signals, 15*time.Second)
	defer cancel()
	records, err := instances.List(ctx, settings.Root(), fallback)
	if err != nil {
		return err
	}
	return printInstances(records, defaultEndpoint, *jsonOutput, stdout)
}
func printInstances(records []instances.Record, defaultEndpoint string, jsonOutput bool, out io.Writer) error {
	views := make([]instanceView, 0, len(records))
	for _, r := range records {
		view := instanceView{PID: r.PID, Default: transport.SameEndpoint(r.Endpoint, defaultEndpoint), Listen: r.Endpoint, Binary: r.Binary, Config: r.Config, LogFile: r.LogFile}
		if !r.Started.IsZero() {
			view.Started = r.Started.Format(time.RFC3339)
		}
		views = append(views, view)
	}
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(views)
	}
	if len(views) == 0 {
		_, err := fmt.Fprintln(out, "No running proxy instances found for the current user.")
		return err
	}
	table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "PID\tINSTANCE\tSOCKET / PIPE\tLOG"); err != nil {
		return err
	}
	for _, v := range views {
		kind := "additional"
		if v.Default {
			kind = "default"
		}
		log := v.LogFile
		if log == "" {
			log = "(stderr / service output)"
		}
		if _, err := fmt.Fprintf(table, "%d\t%s\t%s\t%s\n", v.PID, kind, tableText(v.Listen), tableText(log)); err != nil {
			return err
		}
	}
	return table.Flush()
}
func tableText(value string) string {
	var out strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			fmt.Fprintf(&out, "\\u%04x", r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
