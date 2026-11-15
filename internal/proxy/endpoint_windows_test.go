package proxy

import (
	"testing"

	"keepassxc-agent-middleware/internal/testutil"
)

func testEndpoint(t *testing.T) (string, func()) {
	t.Helper()
	return testutil.PipeEndpoint("kam-proxy-test"), func() {}
}
