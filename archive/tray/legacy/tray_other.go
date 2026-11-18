//go:build !windows

package main

import "errors"

func runTray(args []string) error {
	return errors.New("the tray controller is available on Windows only")
}
