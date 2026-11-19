package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"keepassxc-agent-middleware/internal/settings"
	"os"
	"path/filepath"
	"sort"
)

// atomicWrite refuses symlinks and swaps a completed same-directory temporary
// file into place. Installation roots are private to the current user.
func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	if _, _, err := readRegular(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".keepassxc-agent-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

type snapshot struct {
	path   string
	data   []byte
	mode   fs.FileMode
	exists bool
}
type transaction struct{ before []snapshot }

func (tx *transaction) remove(path string) error {
	data, exists, err := readRegular(path)
	if err != nil || !exists {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tx.before = append(tx.before, snapshot{path: path, data: data, mode: info.Mode().Perm(), exists: true})
	return os.Remove(path)
}

func (tx *transaction) write(path string, data []byte, mode fs.FileMode) error {
	old, exists, err := readRegular(path)
	if err != nil {
		return err
	}
	oldMode := mode
	if exists {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		oldMode = info.Mode().Perm()
		if bytes.Equal(old, data) {
			return nil
		}
	}
	tx.before = append(tx.before, snapshot{path, old, oldMode, exists})
	return atomicWrite(path, data, mode)
}
func (tx *transaction) rollback() error {
	var errs []error
	for i := len(tx.before) - 1; i >= 0; i-- {
		before := tx.before[i]
		if before.exists {
			errs = append(errs, atomicWrite(before.path, before.data, before.mode))
		} else {
			err := os.Remove(before.path)
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (p *Plan) Install(ctx context.Context, manager Manager) (resultErr error) {
	unlock, err := lockRoot(p.Root)
	if err != nil {
		return err
	}
	defer unlock()
	if p.Previous != nil {
		if err := checkFiles(p.Previous.Files); err != nil {
			return err
		}
	}
	if err := p.checkProfiles(); err != nil {
		return err
	}
	// Refuse a stale plan if the SSH config or install manifest changed after preview.
	var before []byte
	if p.managesSSH() {
		before, _, err = readRegular(p.SSHPath)
		if err != nil {
			return err
		}
	}
	if !bytes.Equal(before, p.SSHBefore) {
		return errors.New("SSH configuration changed during installation; run install again")
	}
	current, err := ReadManifest(p.ManifestPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	a, _ := json.Marshal(current)
	b, _ := json.Marshal(p.Previous)
	if !bytes.Equal(a, b) {
		return errors.New("installation changed; run install again")
	}
	for path := range p.Files {
		if p.Previous == nil || p.Previous.Files[path] == "" {
			if _, err := os.Lstat(path); err == nil {
				return fmt.Errorf("unowned file appeared during installation: %s", path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	state, err := manager.State(ctx, p)
	if err != nil {
		return err
	}
	if p.Previous == nil && state != "absent" {
		return errors.New("an unowned service is already registered; refusing to replace it")
	}
	if p.Previous != nil {
		if err := manager.Stop(ctx, p); err != nil {
			return err
		}
	}
	tx := &transaction{}
	committed := false
	rollbackPath := func() error { return nil }
	defer func() {
		if committed {
			return
		}
		// Cleanup needs its own bounded context even if installation timed out.
		cleanupCtx, cancel := cleanupContext()
		defer cancel()
		stopErr := manager.Stop(cleanupCtx, p)
		rollbackErr := errors.Join(rollbackPath(), tx.rollback())
		if p.Previous == nil && p.OS == "linux" {
			if _, ok := manager.(SystemManager); ok {
				_, reloadErr := command(cleanupCtx, "systemctl", "--user", "daemon-reload")
				rollbackErr = errors.Join(rollbackErr, reloadErr)
			}
		}
		var restartErr error
		if p.Previous != nil && rollbackErr == nil {
			previousPlan := *p
			previousPlan.Binary = p.Previous.Binary
			previousPlan.Manifest = *p.Previous
			previousPlan.restoreTray = p.Previous.TrayEnabled
			if config, err := settings.Load(p.ConfigPath); err == nil {
				previousPlan.Config = config
			}
			restartErr = manager.Start(cleanupCtx, &previousPlan)
		}
		resultErr = errors.Join(resultErr, stopErr, rollbackErr, restartErr)
	}()
	paths := make([]string, 0, len(p.Files))
	for path := range p.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		mode := fs.FileMode(0600)
		if path == p.Binary || path == p.serviceBinary() || path == p.Launcher {
			mode = 0700
		}
		if err := tx.write(path, p.Files[path], mode); err != nil {
			return err
		}
	}
	if p.Previous != nil {
		for _, path := range []string{p.TrayServicePath, p.TrayAutostartPath, p.Launcher} {
			if path != "" && p.Previous.Files[path] != "" && p.Files[path] == nil {
				if err := tx.remove(path); err != nil {
					return err
				}
			}
		}
	}
	if !bytes.Equal(p.SSHBefore, p.SSHAfter) {
		if p.Manifest.SSHBackup != "" {
			// A backup from a previous install is deliberately preserved unchanged.
			_, exists, err := readRegular(p.Manifest.SSHBackup)
			if err != nil {
				return err
			}
			if !exists {
				if err := tx.write(p.Manifest.SSHBackup, p.SSHBefore, 0600); err != nil {
					return err
				}
			}
		}
		if err := tx.write(p.SSHPath, p.SSHAfter, 0600); err != nil {
			return err
		}
	}
	if err := p.writeProfiles(tx); err != nil {
		return err
	}
	if _, ok := manager.(SystemManager); ok && p.OS == "windows" {
		rollbackPath, err = p.applyUserPath()
		if err != nil {
			rollbackPath = func() error { return nil }
			return err
		}
	}
	manifest, err := json.MarshalIndent(p.Manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := tx.write(p.ManifestPath, append(manifest, '\n'), 0600); err != nil {
		return err
	}
	if err := manager.Start(ctx, p); err != nil {
		return fmt.Errorf("service startup failed (see %s): %w", p.LogPath, err)
	}
	committed = true
	return nil
}

func (p *Plan) Uninstall(ctx context.Context, manager Manager) error {
	unlock, err := lockRoot(p.Root)
	if err != nil {
		return err
	}
	defer unlock()
	if p.Previous == nil {
		return errors.New("no installation manifest")
	}
	if err := p.checkRemaining(); err != nil {
		return err
	}
	var before []byte
	if p.managesSSH() {
		before, _, err = readRegular(p.SSHPath)
		if err != nil {
			return err
		}
	}
	if !bytes.Equal(before, p.SSHBefore) {
		return errors.New("SSH configuration changed; run uninstall again")
	}
	if err := p.checkProfiles(); err != nil {
		return err
	}
	if err := manager.Stop(ctx, p); err != nil {
		return err
	}
	p.Previous.Uninstalling = true
	saveProgress := func() error {
		data, err := json.MarshalIndent(p.Previous, "", "  ")
		if err != nil {
			return err
		}
		return atomicWrite(p.ManifestPath, append(data, '\n'), 0600)
	}
	if err := saveProgress(); err != nil {
		return err
	}
	// Restore SSH first. If anything later fails, the original SSH agent remains
	// usable and the manifest is retained so uninstall can be diagnosed/retried.
	if !bytes.Equal(p.SSHBefore, p.SSHAfter) {
		if len(p.SSHAfter) == 0 && p.Previous.SSHCreated {
			err = os.Remove(p.SSHPath)
		} else {
			err = atomicWrite(p.SSHPath, p.SSHAfter, 0600)
		}
		if err != nil {
			return err
		}
	}
	if err := p.checkProfiles(); err != nil {
		return err
	}
	pathTx := &transaction{}
	if err := p.writeProfiles(pathTx); err != nil {
		return errors.Join(err, pathTx.rollback())
	}
	if _, ok := manager.(SystemManager); ok && p.OS == "windows" {
		if _, err := p.applyUserPath(); err != nil {
			return errors.Join(err, pathTx.rollback())
		}
	}
	p.Previous.PathEdits = nil
	p.Previous.UserPathAdded = false
	p.Previous.SSHBlock = ""
	if err := saveProgress(); err != nil {
		return err
	}
	for path := range p.Previous.Files {
		if filepath.Dir(path) == filepath.Join(p.Root, "bin") || path == p.Launcher {
			err = removeBinary(path)
		} else {
			err = os.Remove(path)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("service stopped; could not remove %s: %w", path, err)
		}
		delete(p.Previous.Files, path)
		if err := saveProgress(); err != nil {
			return err
		}
	}
	if p.OS == "linux" {
		if _, ok := manager.(SystemManager); ok {
			if _, err := command(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
				return err
			}
		}
	}
	if err = os.Remove(p.ManifestPath); err != nil {
		return err
	}
	// Keep logs and SSH backups; never recursively delete user directories.
	_ = os.Remove(filepath.Join(p.Root, "bin"))
	return nil
}
