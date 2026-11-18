package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
			return fmt.Errorf("open tray (run install first): %w", err)
		}
		return nil
	}
	h := tray.Handlers{
		RuntimeRoot: settings.Root(),
		Snapshot: func() tray.Snapshot {
			var out bytes.Buffer
			err := execute([]string{"status"}, &out, &out)
			details := out.String()
			if err != nil {
				details += "\n" + err.Error()
			}
			manifest, manifestErr := install.ReadManifest(filepath.Join(settings.Root(), "install.json"))
			managed := manifestErr == nil && manifest.Version == 1 && !manifest.Uninstalling
			return tray.Snapshot{
				Installed: managed,
				Proxy:     strings.Contains(out.String(), "proxy: reachable ("),
				Healthy:   err == nil && managed,
				Details:   "Última comprobación: " + time.Now().Format("15:04:05") + "\n\n" + details,
			}
		},
		Control: controlInstalled,
		OpenLogs: func() error {
			path := filepath.Join(settings.Root(), "agent.log")
			if config, err := settings.Load(settings.DefaultPath()); err == nil && config.LogFile != "" {
				path = config.LogFile
			}
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("no se puede abrir el log %s: %w", path, err)
			}
			return openTrayLog(path)
		},
	}
	if err := configureTray(&h); err != nil {
		return err
	}
	err := tray.Run(h)
	if err != nil {
		logTrayError(err)
	}
	return err
}

func logTrayError(err error) {
	if file, e := os.OpenFile(filepath.Join(settings.Root(), "tray.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); e == nil {
		fmt.Fprintf(file, "%s: %v\n", time.Now().Format(time.RFC3339), err)
		file.Close()
	}
}
