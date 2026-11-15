package proxy

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func testEndpoint(t *testing.T) (string, func()) {
	return fmt.Sprintf(`\\.\pipe\kam-proxy-test-%d-%d`, os.Getpid(), time.Now().UnixNano()), func() {}
}
