package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"runtime"
	"time"

	"keepassxc-agent-middleware/internal/install"
	"keepassxc-agent-middleware/internal/settings"
)

func applyConfig(flags *flag.FlagSet, path string, listen, upstream, browserPath, expected *string, timeout *time.Duration, logFile *string) error {
	explicit := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	config, err := settings.Load(path)
	if errors.Is(err, os.ErrNotExist) && !explicit["config"] {
		return nil
	}
	if err != nil {
		return err
	}
	set := func(name string, target *string, value string) {
		if !explicit[name] && value != "" {
			*target = value
		}
	}
	set("listen", listen, config.Listen)
	// An explicit upstream in config wins. Otherwise prefer the current session's
	// environment; the saved fallback handles service managers with a clean env.
	if !explicit["upstream"] {
		if config.Upstream != "" {
			*upstream = config.Upstream
		} else if *upstream == "" {
			*upstream = config.FallbackUpstream
		}
	}
	set("browser", browserPath, config.Browser)
	set("fingerprint", expected, config.Fingerprint)
	set("log-file", logFile, config.LogFile)
	if !explicit["timeout"] && config.Timeout != "" {
		*timeout, err = time.ParseDuration(config.Timeout)
		if err != nil {
			return fmt.Errorf("invalid timeout in %s: %w", path, err)
		}
	}
	return nil
}

func localOptions() (install.Options, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return install.Options{}, err
	}
	current, err := user.Current()
	if err != nil {
		return install.Options{}, err
	}
	if current.Uid == "0" {
		return install.Options{}, errors.New("run install/uninstall as your desktop user, without sudo")
	}
	return install.Options{Home: home, OS: runtime.GOOS, UserID: current.Uid}, nil
}

func installedStatus(out io.Writer) error {
	o, err := localOptions()
	if err != nil {
		return err
	}
	// Status must work even if an owned file was edited; don't run the installer's
	// destructive-operation ownership validation just to report registration.
	m, err := install.ReadManifest(settings.Root() + string(os.PathSeparator) + "install.json")
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(out, "service: not installed")
		return nil
	}
	if err != nil {
		return err
	}
	ctx, cancel := installContext()
	defer cancel()
	p, err := install.Location(o)
	if err != nil {
		return err
	}
	p.Previous = m
	state, err := (install.SystemManager{}).State(ctx, p)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "service: %s (%s)\n", state, m.Binary)
	return nil
}
