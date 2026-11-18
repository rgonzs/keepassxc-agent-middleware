package instances

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"keepassxc-agent-middleware/internal/transport"
)

func serveTestEndpoint(t *testing.T, endpoint string) net.Listener {
	t.Helper()
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	var clients sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			clients.Add(1)
			go func() { defer clients.Done(); defer conn.Close(); io.Copy(io.Discard, conn) }()
		}
	}()
	t.Cleanup(func() { listener.Close(); <-done; clients.Wait() })
	return listener
}
func TestRegistryListsOnlyLiveEndpointsAndCleansOwnRecord(t *testing.T) {
	root := testRoot(t)
	first := testEndpoint(t, root, "first")
	second := testEndpoint(t, root, "second")
	serveTestEndpoint(t, first)
	b := serveTestEndpoint(t, second)
	undoA, err := Register(root, Candidate{Endpoint: first, LogFile: filepath.Join(root, "one.log")})
	if err != nil {
		t.Fatal(err)
	}
	defer undoA()
	undoB, err := Register(root, Candidate{Endpoint: second})
	if err != nil {
		t.Fatal(err)
	}
	defer undoB()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	records, err := List(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d instances: %+v", len(records), records)
	}
	for _, r := range records {
		if r.PID != os.Getpid() || r.Identity == "" || r.Started.IsZero() {
			t.Fatalf("bad metadata: %+v", r)
		}
	}
	stale := records[0]
	stale.Identity += "-old-process"
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(filepath.Join(root, "instances", "stale.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "instances", "broken.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	b.Close()
	records, err = List(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Endpoint != first {
		t.Fatalf("stale or stopped endpoint listed: %+v", records)
	}
	undoA()
	records, err = List(ctx, root, nil)
	if err != nil || len(records) != 0 {
		t.Fatalf("unregister: %+v %v", records, err)
	}
}
func TestDiscoverLegacyDefaultVerifiesExecutable(t *testing.T) {
	root := testRoot(t)
	endpoint := testEndpoint(t, root, "legacy")
	serveTestEndpoint(t, endpoint)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	records, err := List(ctx, root, &Candidate{Endpoint: endpoint, Binary: binary})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].PID != os.Getpid() {
		t.Fatalf("legacy instance not discovered: %+v", records)
	}
	fake := filepath.Join(root, "different-binary")
	if err := os.WriteFile(fake, []byte("not the server"), 0600); err != nil {
		t.Fatal(err)
	}
	records, err = List(ctx, root, &Candidate{Endpoint: endpoint, Binary: fake})
	if err != nil || len(records) != 0 {
		t.Fatalf("unrelated endpoint accepted: %+v %v", records, err)
	}
	undo, err := Register(root, Candidate{Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	defer undo()
	records, err = List(ctx, root, &Candidate{Endpoint: endpoint, Binary: binary})
	if err != nil || len(records) != 1 {
		t.Fatalf("fallback duplicated registry: %+v %v", records, err)
	}
}
func TestRegistryOmitsCrashedProcess(t *testing.T) {
	root := testRoot(t)
	endpoint := testEndpoint(t, root, "child")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestInstanceChild$")
	cmd.Env = append(os.Environ(), "KAM_INSTANCE_CHILD_ROOT="+root, "KAM_INSTANCE_CHILD_ENDPOINT="+endpoint)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && line != "ready\n" {
			err = fmt.Errorf("unexpected child output %q", line)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("child did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	records, err := List(ctx, root, nil)
	if err != nil || len(records) != 1 || records[0].PID != cmd.Process.Pid {
		t.Fatalf("child not listed: %+v %v", records, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	waited = true
	// A forced crash leaves metadata. Kernel identity, not a PID file, decides
	// whether it is shown; no process is signaled by List.
	entries, err := os.ReadDir(filepath.Join(root, "instances"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("missing crash fixture: %v", err)
	}
	records, err = List(ctx, root, nil)
	if err != nil || len(records) != 0 {
		t.Fatalf("crashed process listed: %+v %v", records, err)
	}
}
func TestInstanceChild(t *testing.T) {
	root := os.Getenv("KAM_INSTANCE_CHILD_ROOT")
	if root == "" {
		return
	}
	endpoint := os.Getenv("KAM_INSTANCE_CHILD_ENDPOINT")
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	unregister, err := Register(root, Candidate{Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()
	fmt.Println("ready")
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() { defer conn.Close(); io.Copy(io.Discard, conn) }()
	}
}
