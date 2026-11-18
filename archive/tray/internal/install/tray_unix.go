//go:build !windows

package install

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func trayServiceID(p *Plan) string {
	if p.OS == "darwin" {
		return p.serviceID() + ".tray"
	}
	return "keepassxc-agent-tray.service"
}

func trayState(ctx context.Context, p *Plan, run commandRunner) (string, error) {
	q := *p
	q.job = trayServiceID(p)
	return serviceState(ctx, &q, run)
}

func preflightUnixTray(ctx context.Context, p *Plan) error {
	if !p.EnableTray {
		return nil
	}
	state, err := trayState(ctx, p, command)
	if err != nil {
		return err
	}
	if state != "absent" && (p.Previous == nil || p.Previous.Files[p.TrayServicePath] == "") {
		return errors.New("an unowned tray service is already registered; refusing to replace it")
	}
	return nil
}

func startUnixTray(ctx context.Context, p *Plan, run commandRunner) error {
	if !p.EnableTray {
		return nil
	}
	if err := trustedManagedFile(p, p.TrayServicePath); err != nil {
		return err
	}
	owner, err := unixEndpointOwner(ctx, p, filepath.Join(p.Root, "tray.sock"))
	if err != nil {
		return err
	}
	if owner != nil {
		defer owner.close()
		owner.connection.SetDeadline(time.Now().Add(time.Second))
		fmt.Fprintln(owner.connection, "status")
		greeting, err := bufio.NewReader(owner.connection).ReadString('\n')
		if err != nil || greeting != "keepassxc-agent-tray-v1\n" {
			return errors.New("tray endpoint is occupied by another service")
		}
		return nil
	}
	if p.OS == "darwin" {
		target := "gui/" + p.UserID + "/" + trayServiceID(p)
		state, err := trayState(ctx, p, run)
		if err != nil {
			return err
		}
		if _, err := run(ctx, "launchctl", "enable", target); err != nil {
			return err
		}
		if state == "absent" {
			_, err = run(ctx, "launchctl", "bootstrap", "gui/"+p.UserID, p.TrayServicePath)
		} else {
			_, err = run(ctx, "launchctl", "kickstart", target)
		}
		return err
	}
	if _, err := run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	_, err = run(ctx, "systemctl", "--user", "start", trayServiceID(p))
	return err
}

func stopUnixTray(ctx context.Context, p *Plan, run commandRunner) error {
	managed := p.Manifest.TrayEnabled || (p.Previous != nil && p.Previous.TrayEnabled)
	if m, err := ReadManifest(p.ManifestPath); err == nil {
		managed = managed || m.TrayEnabled
	}
	if !managed {
		return nil
	}
	owner, err := unixEndpointOwner(ctx, p, filepath.Join(p.Root, "tray.sock"))
	if err != nil {
		return err
	}
	if owner != nil {
		defer owner.close()
		owner.connection.SetDeadline(time.Now().Add(time.Second))
		fmt.Fprintln(owner.connection, "status")
		greeting, err := bufio.NewReader(owner.connection).ReadString('\n')
		if err != nil || greeting != "keepassxc-agent-tray-v1\n" {
			return errors.New("cannot prove the process at tray.sock is a desktop controller")
		}
	}
	state := "absent"
	if !removedDuringUninstall(p, p.TrayServicePath) {
		state, err = trayState(ctx, p, run)
	}
	if err != nil {
		return err
	}
	if state != "absent" {
		if err := trustedManagedFile(p, p.TrayServicePath); err != nil {
			return err
		}
		if p.OS == "darwin" {
			_, err = run(ctx, "launchctl", "bootout", "gui/"+p.UserID+"/"+trayServiceID(p))
		} else {
			_, err = run(ctx, "systemctl", "--user", "stop", "--no-block", trayServiceID(p))
		}
		if err != nil {
			return err
		}
	}
	if owner != nil {
		if err := owner.stop(ctx); err != nil {
			return err
		}
		owner.connection.Close()
	}
	if state != "absent" && p.OS == "linux" {
		q := *p
		q.job = trayServiceID(p)
		if err := waitUnitStopped(ctx, &q, run); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(p.Root, "tray.sock")); err == nil {
		return waitEndpoint(ctx, filepath.Join(p.Root, "tray.sock"), false)
	}
	return nil
}

func removedDuringUninstall(p *Plan, path string) bool {
	if p.Previous != nil && p.Previous.Uninstalling && p.Previous.Files[path] == "" {
		return true
	}
	if m, err := ReadManifest(p.ManifestPath); err == nil && m.Uninstalling && m.Files[path] == "" {
		return true
	}
	return false
}

func openUnixTray(ctx context.Context, p *Plan) error {
	m, err := ReadManifest(p.ManifestPath)
	if err != nil {
		return err
	}
	if err := p.validateManifest(m); err != nil {
		return err
	}
	if !m.TrayEnabled {
		return errors.New("desktop controller is not installed; install the full package without --no-tray")
	}
	p.Previous = m
	p.EnableTray = true
	p.Binary = m.Binary
	p.NativeHelper = m.NativeHelper
	return startUnixTray(ctx, p, command)
}
