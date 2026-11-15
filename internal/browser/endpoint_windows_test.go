package browser

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func testEndpoint(t *testing.T) (string, func()) {
	return fmt.Sprintf(`\\.\pipe\kam-browser-test-%d-%d`, os.Getpid(), time.Now().UnixNano()), func() {}
}
