//go:build !windows

package install

import (
	"os"
	"os/exec"
)

func hideWindow(cmd *exec.Cmd)       {}
func removeBinary(path string) error { return os.Remove(path) }
