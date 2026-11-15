//go:build !windows

package browser

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

func DefaultEndpoint() string {
	const name = "org.keepassxc.KeePassXC.BrowserServer"
	if runtime.GOOS == "darwin" {
		if output, err := exec.Command("/usr/bin/getconf", "DARWIN_USER_TEMP_DIR").Output(); err == nil {
			return filepath.Join(strings.TrimSpace(string(output)), name)
		}
		return filepath.Join(os.TempDir(), name)
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		name := os.Getenv("USER")
		if current, err := user.Current(); err == nil {
			name = current.Username
		}
		dir = filepath.Join(os.TempDir(), "runtime-"+name)
	}
	candidates := []string{
		filepath.Join(dir, "app", "org.keepassxc.KeePassXC", name),
		filepath.Join(dir, name),
	}
	if snap := os.Getenv("SNAP_USER_COMMON"); snap != "" {
		candidates = append([]string{filepath.Join(snap, name)}, candidates...)
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return candidates[0]
}
