package testutil

import (
	"sync"
	"testing"
)

func TestPipeEndpointsAreUniqueAcrossConcurrentCalls(t *testing.T) {
	const workers = 16
	const perWorker = 128
	endpoints := make(chan string, workers*perWorker)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perWorker {
				endpoints <- PipeEndpoint("kam-test")
			}
		}()
	}
	wg.Wait()
	close(endpoints)
	seen := make(map[string]bool, workers*perWorker)
	for endpoint := range endpoints {
		if seen[endpoint] {
			t.Fatalf("duplicate pipe endpoint: %s", endpoint)
		}
		seen[endpoint] = true
	}
}
