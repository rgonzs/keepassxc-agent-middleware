//go:build !windows

package transport

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestSocketProtection(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "kam-transport-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "agent.sock")
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("socket is not private")
	}
	if second, err := Listen(path); err == nil {
		second.Close()
		t.Fatal("replaced live socket")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if second, err := Listen(filepath.Join(dir, "unsafe.sock")); err == nil {
		second.Close()
		t.Fatal("accepted public directory")
	}
}

func TestDetectsSymlinkLoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "original")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if !SameEndpoint(alias, path) {
		t.Fatal("symlink loop not detected")
	}
}

func TestRecoversOnlyStaleSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "kam-stale-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "agent.sock")
	original, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	original.SetUnlinkOnClose(false)
	// A live endpoint without our lock must still be preserved.
	if proxy, err := Listen(path); err == nil {
		proxy.Close()
		t.Fatal("removed a live external socket")
	}
	original.Close()
	recovered, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	recovered.Close()
	if err := os.WriteFile(path, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if proxy, err := Listen(path); err == nil {
		proxy.Close()
		t.Fatal("removed an unrelated file")
	}
}
