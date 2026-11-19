package localfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAppendCreatesAndPreservesLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	for _, text := range []string{"first\n", "second\n"} {
		file, err := OpenAppend(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.WriteString(text)
		if err := errors.Join(err, file.Close()); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "first\nsecond\n" {
		t.Fatalf("log = %q, %v", data, err)
	}
}

func TestRefuseSymbolicLinks(t *testing.T) {
	dir := t.TempDir()
	target, link := filepath.Join(dir, "target"), filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symbolic link: %v", err)
	}
	if _, err := OpenAppend(link); !errors.Is(err, errNotRegular) {
		t.Fatalf("opening linked log for append: %v", err)
	}
	if _, _, err := Open(link); !errors.Is(err, errNotRegular) {
		t.Fatalf("opening linked log for read: %v", err)
	}
	if _, err := ReadConfig(link); !errors.Is(err, errNotRegular) {
		t.Fatalf("opening linked configuration: %v", err)
	}
	// The OS open must also reject a link that appears after the path check.
	for _, appendLog := range []bool{false, true} {
		if file, err := openFile(link, appendLog); err == nil {
			file.Close()
			t.Fatalf("platform open followed the link (append=%v)", appendLog)
		}
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAppend(link); !errors.Is(err, errNotRegular) {
		t.Fatalf("opening dangling link: %v", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dangling target was created: %v", err)
	}
}
