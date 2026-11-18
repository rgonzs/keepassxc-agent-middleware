//go:build !windows

package instances

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "kam-instances-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return root
}
func testEndpoint(t *testing.T, root, name string) string {
	t.Helper()
	return filepath.Join(root, name+".sock")
}

func TestRelativeEndpointIsRecordedAsAbsolute(t *testing.T) {
	root := testRoot(t)
	t.Chdir(root)
	serveTestEndpoint(t, "relative.sock")
	undo, err := Register(root, Candidate{Endpoint: "relative.sock"})
	if err != nil {
		t.Fatal(err)
	}
	defer undo()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	records, err := List(ctx, root, nil)
	if err != nil || len(records) != 1 || records[0].Endpoint != filepath.Join(root, "relative.sock") {
		t.Fatalf("relative endpoint discovery: %+v %v", records, err)
	}
}
