//go:build !windows

package main

import (
	"os/exec"
	"runtime"

	"keepassxc-agent-middleware/internal/install"
	"keepassxc-agent-middleware/internal/tray"
)

func configureTray(h *tray.Handlers) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	o, err := localOptions()
	if err != nil {
		return err
	}
	p, err := install.Location(o)
	if err != nil {
		return err
	}
	h.NativeHelper, err = p.NativeTrayExecutable()
	return err
}

func openTrayLog(path string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "/usr/bin/open"
	}
	cmd := exec.Command(name, path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			logTrayError(err)
		}
	}()
	return nil
}
