//go:build !windows

package browser

import (
	"os"
	"path/filepath"
	"testing"
)

func testEndpoint(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "kam-browser-")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "browser.sock"), func() { os.RemoveAll(dir) }
}
