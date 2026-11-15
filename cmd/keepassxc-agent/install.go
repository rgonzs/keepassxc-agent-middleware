package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"keepassxc-agent-middleware/internal/browser"
	"keepassxc-agent-middleware/internal/install"
	"keepassxc-agent-middleware/internal/settings"
	"keepassxc-agent-middleware/internal/transport"
)

func installContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func installation(command string, args []string, stdout, stderr io.Writer) error {
	o, err := localOptions()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dry := flags.Bool("dry-run", false, "preview all changes without writing files or invoking a service manager")
	var noSSH *bool
	var listen, upstream, browserPath, expected *string
	var timeout *time.Duration
	if command == "install" {
		noSSH = flags.Bool("no-ssh", false, "do not configure SSH automatically")
		endpoint := transport.DefaultListen()
		if o.OS != "windows" {
			endpoint = filepath.Join(o.Home, ".keepassxc-agent", "agent.sock")
		}
		listen = flags.String("listen", endpoint, "stable proxy endpoint")
		upstream = flags.String("upstream", transport.DefaultUpstream(), "original SSH agent; explicit value is pinned")
		browserPath = flags.String("browser", browser.DefaultEndpoint(), "KeePassXC endpoint; explicit value is pinned")
		expected = flags.String("fingerprint", "", "expected SHA256 fingerprint")
		timeout = flags.Duration("timeout", 60*time.Second, "unlock timeout")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	ctx, cancel := installContext()
	defer cancel()
	manager := install.SystemManager{}
	if command == "uninstall" {
		p, err := install.Existing(o)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stdout, "No managed installation found.")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Stop service and remove %d managed files.\nRemove managed SSH block from %s.\nPreserve logs, backups and unrelated SSH edits.\n", len(p.Previous.Files), p.SSHPath)
		if *dry {
			return nil
		}
		if err := p.Uninstall(ctx, manager); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Uninstalled. Logs and SSH backups were preserved.")
		return nil
	}
	previous, loadErr := settings.Load(settings.DefaultPath())
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	}

	if *upstream == "" && loadErr == nil {
		explicitlyEmpty := false
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "upstream" {
				explicitlyEmpty = true
			}
		})
		if !explicitlyEmpty {
			*upstream = previous.Upstream
			if *upstream == "" {
				*upstream = previous.FallbackUpstream
			}
		}
	}
	if *listen == "" || *upstream == "" || *browserPath == "" {
		return errors.New("listen, upstream and browser endpoints must be set (use --upstream if SSH_AUTH_SOCK is unset)")
	}
	if o.OS != "windows" && (!filepath.IsAbs(*listen) || !filepath.IsAbs(*upstream) || !filepath.IsAbs(*browserPath)) {
		return errors.New("service socket paths must be absolute")
	}
	if transport.SameEndpoint(*listen, *upstream) || transport.SameEndpoint(*listen, *browserPath) {
		return errors.New("proxy endpoint must differ from upstream and browser endpoints")
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if *expected != "" {
		data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(*expected, "SHA256:"))
		if !strings.HasPrefix(*expected, "SHA256:") || err != nil || len(data) != 32 {
			return errors.New("fingerprint must be an OpenSSH SHA256 fingerprint")
		}
	}
	explicit := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	o.Source, err = os.Executable()
	if err != nil {
		return err
	}
	o.Config = settings.Config{Listen: *listen, Timeout: timeout.String(), Fingerprint: *expected}
	o.ConfigureSSH = !*noSSH
	// Preserve the original explicit upstream/browser policy on repeated installs.
	if loadErr == nil {
		o.Config = previous
		o.Config.LogFile = ""
		if explicit["listen"] {
			o.Config.Listen = *listen
		}
		if explicit["timeout"] {
			o.Config.Timeout = timeout.String()
		}
		if explicit["fingerprint"] {
			o.Config.Fingerprint = *expected
		}
	}
	if explicit["upstream"] {
		o.Config.Upstream = *upstream
	}
	if explicit["browser"] {
		o.Config.Browser = *browserPath
	}
	o.Config.FallbackUpstream = *upstream
	p, err := install.NewPlan(o)
	if err != nil {
		return err
	}
	// Validate effective saved settings too, before any service or SSH change.
	if (p.Config.Browser != "" && transport.SameEndpoint(p.Config.Listen, p.Config.Browser)) || transport.SameEndpoint(p.Config.Listen, *upstream) || (p.Config.Upstream != "" && transport.SameEndpoint(p.Config.Listen, p.Config.Upstream)) {
		return errors.New("installed upstream points back to the proxy")
	}
	fmt.Fprint(stdout, p.Summary())
	if *dry {
		fmt.Fprintln(stdout, "\nService definition preview:")
		fmt.Fprintln(stdout, string(p.Files[p.ServicePath]))
		if p.Manifest.SSHBlock != "" {
			fmt.Fprintln(stdout, "SSH block preview:\n"+p.Manifest.SSHBlock)
		}
		return nil
	}
	if err := p.Install(ctx, manager); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Installed and started for your user. Run keepassxc-agent status to check connectivity.")
	return nil
}
