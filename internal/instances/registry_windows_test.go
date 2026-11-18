package instances

import (
	"keepassxc-agent-middleware/internal/testutil"
	"testing"
)

func testRoot(t *testing.T) string { t.Helper(); return t.TempDir() }
func testEndpoint(t *testing.T, root, name string) string {
	t.Helper()
	return testutil.PipeEndpoint("kam-instances-" + name)
}
