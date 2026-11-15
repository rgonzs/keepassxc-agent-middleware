//go:build !windows

package proxy

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"keepassxc-agent-middleware/internal/transport"
)

// Exercise the actual OpenSSH agent and ssh-add, in addition to protocol mocks.
// Keys are generated just for this test and never touch the user's agent.
func TestNativeOpenSSH(t *testing.T) {
	agentBin, err := exec.LookPath("ssh-agent")
	if err != nil {
		t.Skip("ssh-agent unavailable")
	}
	addBin, err := exec.LookPath("ssh-add")
	if err != nil {
		t.Skip("ssh-add unavailable")
	}
	upstream, cleanup := testEndpoint(t)
	defer cleanup()
	front, cleanup := testEndpoint(t)
	defer cleanup()
	process := exec.Command(agentBin, "-D", "-a", upstream)
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Process.Kill(); _ = process.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		conn, err := transport.Dial(ctx, upstream)
		if err == nil {
			conn.Close()
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("agent did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	private, public := key(t)
	unlock := unlockFunc(func(ctx context.Context) error {
		conn, err := transport.Dial(ctx, upstream)
		if err != nil {
			return err
		}
		defer conn.Close()
		return agent.NewClient(conn).Add(agent.AddedKey{PrivateKey: private, Comment: "middleware-test"})
	})
	listener, err := transport.Listen(front)
	if err != nil {
		t.Fatal(err)
	}
	server := Server{Upstream: upstream, Unlocker: unlock, Timeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	serveCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(serveCtx, listener) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	command := exec.CommandContext(ctx, addBin, "-L")
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "SSH_AUTH_SOCK=") {
			command.Env = append(command.Env, env)
		}
	}
	command.Env = append(command.Env, "SSH_AUTH_SOCK="+front)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-add failed: %v: %s", err, output)
	}
	expected := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public)))
	if !strings.Contains(string(output), expected) {
		t.Fatalf("ssh-add did not see test key: %s", output)
	}
	c, conn := client(t, front)
	defer conn.Close()
	signature, err := c.Sign(public, []byte("native agent test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := public.Verify([]byte("native agent test"), signature); err != nil {
		t.Fatal(err)
	}
}
