package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"keepassxc-agent-middleware/internal/install"
	"keepassxc-agent-middleware/internal/settings"
	"keepassxc-agent-middleware/internal/tray"
)

func runTray(args []string) error {
	background := len(args) == 1 && args[0] == "--background"
	if len(args) != 0 && !background {
		return errors.New("usage: keepassxc-agent tray")
	}
	if !background {
		o, err := localOptions()
		if err != nil {
			return err
		}
		p, err := install.Location(o)
		if err != nil {
			return err
		}
		ctx, cancel := installContext()
		defer cancel()
		if err := (install.SystemManager{}).OpenTray(ctx, p); err != nil {
			return fmt.Errorf("open tray (run install first to register its task): %w", err)
		}
		return nil
	}
	err := tray.Run(tray.Handlers{
		Snapshot: func() tray.Snapshot {
			var out bytes.Buffer
			err := execute([]string{"status"}, &out, &out)
			details := out.String()
			if err != nil {
				details += "\n" + err.Error()
			}
			return tray.Snapshot{
				Installed: strings.Contains(out.String(), "service: registered ("),
				Proxy:     strings.Contains(out.String(), "proxy: reachable ("),
				Healthy:   err == nil && strings.Contains(out.String(), "service: registered ("),
				Details:   "Última comprobación: " + time.Now().Format("15:04:05") + "\n\n" + details,
			}
		},
		Control: func(action string) error {
			o, err := localOptions()
			if err != nil {
				return err
			}
			p, err := install.Location(o)
			if err != nil {
				return err
			}
			p.Config, err = settings.Load(settings.DefaultPath())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return (install.SystemManager{}).Control(ctx, p, action)
		},
		OpenLogs: func() error {
			path := filepath.Join(settings.Root(), "agent.log")
			config, err := settings.Load(settings.DefaultPath())
			if err == nil && config.LogFile != "" {
				path = config.LogFile
			}
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("no se puede abrir el log %s: %w", path, err)
			}
			file, err := windows.UTF16PtrFromString(path)
			if err != nil {
				return err
			}
			return windows.ShellExecute(0, nil, file, nil, nil, 1)
		},
	})
	if err != nil {
		// Startup failures have no console. Keep them available for diagnosis.
		if file, e := os.OpenFile(filepath.Join(settings.Root(), "tray.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); e == nil {
			fmt.Fprintf(file, "%s: %v\n", time.Now().Format(time.RFC3339), err)
			file.Close()
		}
	}
	return err
}
