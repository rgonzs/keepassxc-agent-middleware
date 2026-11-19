package localfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectMacOSExtendedACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/bin/chmod", "+a", "everyone allow write", path).CombinedOutput(); err != nil {
		t.Fatalf("set ACL: %v: %s", err, output)
	}
	if _, err := ReadConfig(path); err == nil || !strings.Contains(err.Error(), "macOS extended ACL") {
		t.Fatalf("reading configuration with ACL: %v", err)
	}
}
