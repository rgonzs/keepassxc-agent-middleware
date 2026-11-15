//go:build !windows

package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

func testEndpoint(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "kam-proxy-")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "agent.sock"), func() { os.RemoveAll(dir) }
}
