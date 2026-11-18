//go:build !windows

package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"keepassxc-agent-middleware/internal/transport"
)

func TestWaitsForActualEndpointRelease(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "kam-lifecycle-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	endpoint := filepath.Join(dir, "proxy.sock")
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := waitEndpoint(ctx, endpoint, true); err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(150 * time.Millisecond); listener.Close() }()
	if err := waitEndpoint(ctx, endpoint, false); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(dir, strings.Repeat("x", 300))
	if err := waitEndpoint(ctx, invalid, false); err == nil {
		t.Fatal("probe failure was mistaken for successful shutdown")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if err := waitEndpoint(canceled, endpoint, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was ignored: %v", err)
	}
}
