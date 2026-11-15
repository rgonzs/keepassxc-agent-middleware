package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
func removeBinary(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return err
	}
	// An executable cannot delete its own mapped image on Windows. A short-lived
	// helper waits for this CLI to exit, then deletes only this exact version.
	current, e := os.Executable()
	if e != nil {
		return err
	}
	a, e := os.Stat(current)
	if e != nil {
		return err
	}
	b, e := os.Stat(path)
	if e != nil || !os.SameFile(a, b) {
		return err
	}
	script := fmt.Sprintf("Wait-Process -Id %d -ErrorAction SilentlyContinue; Remove-Item -LiteralPath %s -Force", os.Getpid(), ps(path))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", encodedScript(script))
	hideWindow(cmd)
	if e := cmd.Start(); e != nil {
		return e
	}
	return cmd.Process.Release()
}
