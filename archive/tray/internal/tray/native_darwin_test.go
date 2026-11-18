package tray

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// Exercise the real JSON bridge with a protocol peer, without opening a menu
// or interacting with the user's services, SSH agent or KeePassXC.
func TestAppKitBridgeLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "helper")
	marker := filepath.Join(root, "closed")
	script := `#!/bin/sh
set -eu
wait_state() {
 while IFS= read -r line; do
  case "$line" in
   *'"busy":false'*) case "$line" in *"\"proxy\":$1"*) return 0;; esac;;
  esac
 done
 exit 9
}
wait_state true
printf '%s\n' '{"action":"stop"}'
wait_state false
printf '%s\n' '{"action":"start"}'
wait_state true
printf '%s\n' '{"action":"quit"}'
cat >/dev/null
printf done > '` + marker + `'
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var active atomic.Bool
	active.Store(true)
	var controls atomic.Int32
	err := Run(Handlers{
		RuntimeRoot: root, NativeHelper: helper,
		Snapshot: func() Snapshot { return Snapshot{Installed: true, Proxy: active.Load(), Healthy: true} },
		Control:  func(action string) error { controls.Add(1); active.Store(action == "start"); return nil },
		OpenLogs: func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !active.Load() || controls.Load() != 2 {
		t.Fatalf("quit changed proxy: active=%v controls=%d", active.Load(), controls.Load())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("helper did not receive EOF: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tray.sock")); !os.IsNotExist(err) {
		t.Fatalf("control socket remains: %v", err)
	}
}
