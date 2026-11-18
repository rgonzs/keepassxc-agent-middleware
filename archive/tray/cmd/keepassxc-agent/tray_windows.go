package main

import (
	"golang.org/x/sys/windows"
	"keepassxc-agent-middleware/internal/tray"
)

func configureTray(h *tray.Handlers) error { return nil }
func openTrayLog(path string) error {
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, nil, file, nil, nil, 1)
}
