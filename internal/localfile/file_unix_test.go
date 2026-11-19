//go:build !windows

package localfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReadConfigUnixPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0600, 0644, 0620, 0602} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		data, err := ReadConfig(path)
		if mode&0022 == 0 {
			if err != nil || string(data) != "{}" {
				t.Errorf("read with mode %04o: %q, %v", mode, data, err)
			}
		} else if err == nil {
			t.Errorf("accepted writable mode %04o", mode)
		}
	}
}

func TestRefuseFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAppend(path); !errors.Is(err, errNotRegular) {
		t.Fatalf("opening FIFO for append: %v", err)
	}
	if _, err := ReadConfig(path); !errors.Is(err, errNotRegular) {
		t.Fatalf("opening FIFO for read: %v", err)
	}
}
