package transport

import (
	"keepassxc-agent-middleware/internal/testutil"
	"testing"
)

func TestPipeInstancesAreExclusivePerEndpoint(t *testing.T) {
	first := testutil.PipeEndpoint("kam-exclusive")
	second := testutil.PipeEndpoint("kam-independent")
	a, err := Listen(first)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if duplicate, err := Listen(first); err == nil {
		duplicate.Close()
		t.Fatal("same pipe accepted a second proxy")
	}
	b, err := Listen(second)
	if err != nil {
		t.Fatalf("different pipe rejected: %v", err)
	}
	defer b.Close()
}
