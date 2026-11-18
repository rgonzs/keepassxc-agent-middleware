//go:build !windows

package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"keepassxc-agent-middleware/internal/settings"
)

func trustedServiceDefinition(p *Plan) error {
	return trustedManagedFile(p, p.ServicePath)
}

func trustedManagedFile(p *Plan, path string) error {
	data, exists, err := readRegular(path)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("managed service definition is missing: %s", path)
	}
	manifests := []*Manifest{p.Previous, &p.Manifest}
	if m, err := ReadManifest(p.ManifestPath); err == nil {
		manifests = append(manifests, m)
	}
	for _, m := range manifests {
		if m != nil && p.validateManifest(m) == nil && m.Files[path] == digest(data) {
			return nil
		}
	}
	return fmt.Errorf("refusing to control an unowned or changed service definition: %s", path)
}

func stopUnix(ctx context.Context, p *Plan, remove bool, run commandRunner) error {
	if remove {
		if err := stopLegacyTray(ctx, p, run); err != nil {
			return err
		}
	}
	state := "absent"
	var err error
	if !removedDuringUninstall(p, p.ServicePath) {
		state, err = serviceState(ctx, p, run)
	}
	if err != nil {
		return err
	}
	endpoint := p.Config.Listen
	if config, err := settings.Load(p.ConfigPath); err == nil {
		endpoint = config.Listen
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var owner *unixProcess
	if endpoint != "" {
		owner, err = unixEndpointOwner(ctx, p, endpoint)
		if err != nil {
			return err
		}
		if owner != nil {
			defer owner.close()
		}
	}
	registered := state != "absent"
	if registered {
		if err := trustedServiceDefinition(p); err != nil {
			return err
		}
	}
	target := "gui/" + p.UserID + "/" + p.serviceID()
	if registered {
		switch p.OS {
		case "darwin":
			// bootout removes supervision before any orphan fallback. Keep the
			// plist and existing enable/disable preference untouched for login.
			if _, err := run(ctx, "launchctl", "bootout", target); err != nil {
				return err
			}
		case "linux":
			if _, err := run(ctx, "systemctl", "--user", "stop", "--no-block", p.serviceID()); err != nil {
				return err
			}
		default:
			return errors.New("unsupported service manager")
		}
	}
	if owner != nil {
		if err := owner.stop(ctx); err != nil {
			return err
		}
		owner.connection.Close()
	}
	if registered {
		if p.OS == "linux" {
			if err := waitUnitStopped(ctx, p, run); err != nil {
				return err
			}
			if remove {
				if _, err := run(ctx, "systemctl", "--user", "disable", p.serviceID()); err != nil {
					return err
				}
			}
		}
	}
	if endpoint != "" {
		return waitEndpoint(ctx, endpoint, false)
	}
	return nil
}

func waitUnitStopped(parent context.Context, p *Plan, run commandRunner) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	for {
		state, err := run(ctx, "systemctl", "--user", "show", p.serviceID(), "--property=ActiveState", "--value")
		if err != nil {
			return err
		}
		if state == "inactive" || state == "failed" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("service did not finish stopping: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func startUnixProxy(ctx context.Context, p *Plan, run commandRunner) error {
	if err := trustedServiceDefinition(p); err != nil {
		return err
	}
	owner, err := unixEndpointOwner(ctx, p, p.Config.Listen)
	if err != nil {
		return err
	}
	if owner != nil {
		owner.close()
		return nil
	}
	if p.OS == "darwin" {
		target := "gui/" + p.UserID + "/" + p.serviceID()
		state, err := serviceState(ctx, p, run)
		if err != nil {
			return err
		}
		if _, err := run(ctx, "launchctl", "enable", target); err != nil {
			return err
		}
		if state == "absent" {
			if _, err := run(ctx, "launchctl", "bootstrap", "gui/"+p.UserID, p.ServicePath); err != nil {
				return err
			}
		} else {
			if _, err := run(ctx, "launchctl", "kickstart", target); err != nil {
				if ready := waitEndpoint(ctx, p.Config.Listen, true); ready != nil {
					return err
				}
			}
		}
	} else if p.OS == "linux" {
		if _, err := run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if _, err := run(ctx, "systemctl", "--user", "start", p.serviceID()); err != nil {
			return err
		}
	} else {
		return errors.New("unsupported service manager")
	}
	if err := waitEndpoint(ctx, p.Config.Listen, true); err != nil {
		return err
	}
	return verifyStartedProxy(ctx, p)
}
