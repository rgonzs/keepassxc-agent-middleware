// Package testutil contains helpers shared by isolated integration tests.
package testutil

import (
	"fmt"
	"os"
	"sync/atomic"
)

var pipeSequence atomic.Uint64

// PipeEndpoint is unique across calls and concurrent test processes. Clock
// timestamps are not identifiers: consecutive Windows clock reads can coincide.
func PipeEndpoint(prefix string) string {
	return fmt.Sprintf(`\\.\pipe\%s-%d-%d`, prefix, os.Getpid(), pipeSequence.Add(1))
}
