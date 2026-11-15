//go:build !windows

package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNoSSHLeavesSymlinkConfigUntouched(t *testing.T) {
	o := fixture(t, "linux")
	o.ConfigureSSH = false
	actual := filepath.Join(o.Home, "dotfiles-ssh-config")
	original := []byte("Host example\n    User example\n")
	mustWrite(t, actual, original)
	path := filepath.Join(o.Home, ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, path); err != nil {
		t.Fatal(err)
	}
	p := mustPlan(t, o)
	manager := &fakeManager{}
	if err := p.Install(context.Background(), manager); err != nil {
		t.Fatal(err)
	}
	removal, err := Existing(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := removal.Uninstall(context.Background(), manager); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, path)) != string(original) {
		t.Fatal("symlink target changed")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced")
	}
}
