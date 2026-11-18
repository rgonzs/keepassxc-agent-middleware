package lifecycle

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestCooperativeStop(t *testing.T) {
	ctx, close, err := Context(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	sent, err := RequestStop(uint32(os.Getpid()))
	if err != nil || !sent {
		t.Fatalf("stop event missing: %t %v", sent, err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("stop request did not cancel context")
	}
}
