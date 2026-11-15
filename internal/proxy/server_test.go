package proxy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"keepassxc-agent-middleware/internal/transport"
)

type unlockFunc func(context.Context) error

func (f unlockFunc) Unlock(ctx context.Context) error { return f(ctx) }

func key(t *testing.T) (ed25519.PrivateKey, ssh.PublicKey) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := ssh.NewPublicKey(private.Public())
	if err != nil {
		t.Fatal(err)
	}
	return private, public
}

func start(t *testing.T, keyring agent.Agent, unlock Unlocker, expected string) string {
	t.Helper()
	upstream, cleanup := testEndpoint(t)
	t.Cleanup(cleanup)
	front, cleanup := testEndpoint(t)
	t.Cleanup(cleanup)
	original, err := transport.Listen(upstream)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			conn, err := original.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _ = agent.ServeAgent(keyring, conn) }()
		}
	}()
	listener, err := transport.Listen(front)
	if err != nil {
		t.Fatal(err)
	}
	server := Server{Upstream: upstream, Fingerprint: expected, Unlocker: unlock, Timeout: 2 * time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		original.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("shutdown hung")
		}
	})
	return front
}

func client(t *testing.T, endpoint string) (agent.ExtendedAgent, net.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := transport.Dial(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	t.Cleanup(func() { conn.Close() })
	return agent.NewClient(conn), conn
}

func TestConcurrentUnlockAndSignature(t *testing.T) {
	ring := agent.NewKeyring()
	private, public := key(t)
	var calls atomic.Int32
	endpoint := start(t, ring, unlockFunc(func(ctx context.Context) error {
		calls.Add(1)
		// Key insertion is asynchronous even after the browser confirms unlock.
		go func() { time.Sleep(50 * time.Millisecond); _ = ring.Add(agent.AddedKey{PrivateKey: private}) }()
		return nil
	}), "")
	const count = 8
	clients := make([]agent.ExtendedAgent, count)
	for i := range clients {
		clients[i], _ = client(t, endpoint)
	}
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			keys, err := c.List()
			if err != nil || len(keys) != 1 {
				t.Errorf("List: %v, %d keys", err, len(keys))
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("got %d unlocks", calls.Load())
	}
	signature, err := clients[0].Sign(public, []byte("verify proxy signature"))
	if err != nil {
		t.Fatal(err)
	}
	if err = public.Verify([]byte("verify proxy signature"), signature); err != nil {
		t.Fatal(err)
	}
	// Agent mutation still passes through to the real agent.
	if err := clients[0].Remove(public); err != nil {
		t.Fatal(err)
	}
	keys, _ := ring.List()
	if len(keys) != 0 {
		t.Fatal("remove not forwarded")
	}
}

func TestExpectedKeyMissingAmongOtherKeys(t *testing.T) {
	ring := agent.NewKeyring()
	other, _ := key(t)
	_ = ring.Add(agent.AddedKey{PrivateKey: other})
	wanted, public := key(t)
	var calls atomic.Int32
	endpoint := start(t, ring, unlockFunc(func(context.Context) error { calls.Add(1); return ring.Add(agent.AddedKey{PrivateKey: wanted}) }), fingerprint(public.Marshal()))
	c, _ := client(t, endpoint)
	keys, err := c.List()
	if err != nil || len(keys) != 2 || calls.Load() != 1 {
		t.Fatalf("keys=%d calls=%d err=%v", len(keys), calls.Load(), err)
	}
}

func TestMissingSigningKeyUnlocks(t *testing.T) {
	ring := agent.NewKeyring()
	private, public := key(t)
	var calls atomic.Int32
	endpoint := start(t, ring, unlockFunc(func(context.Context) error { calls.Add(1); return ring.Add(agent.AddedKey{PrivateKey: private}) }), "")
	c, _ := client(t, endpoint)
	sig, err := c.Sign(public, []byte("sign without listing first"))
	if err != nil {
		t.Fatal(err)
	}
	if err := public.Verify([]byte("sign without listing first"), sig); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("no unlock")
	}
}

type refusingAgent struct{ agent.Agent }

func (refusingAgent) Sign(ssh.PublicKey, []byte) (*ssh.Signature, error) {
	return nil, errors.New("policy denied")
}

func TestRefusedSignatureDoesNotUnlock(t *testing.T) {
	ring := agent.NewKeyring()
	private, public := key(t)
	_ = ring.Add(agent.AddedKey{PrivateKey: private})
	var calls atomic.Int32
	endpoint := start(t, refusingAgent{ring}, unlockFunc(func(context.Context) error { calls.Add(1); return nil }), "")
	c, _ := client(t, endpoint)
	if _, err := c.Sign(public, []byte("refused")); err == nil {
		t.Fatal("signature should fail")
	}
	if calls.Load() != 0 {
		t.Fatal("policy rejection triggered unlock")
	}
}

func TestFailedUnlockCooldown(t *testing.T) {
	var calls atomic.Int32
	endpoint := start(t, agent.NewKeyring(), unlockFunc(func(context.Context) error { calls.Add(1); return errors.New("cancelled") }), "")
	c, _ := client(t, endpoint)
	for range 2 {
		keys, err := c.List()
		if err != nil || len(keys) != 0 {
			t.Fatal("original empty response not preserved")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("prompt storm: %d", calls.Load())
	}
}

func TestShutdownDuringUnlock(t *testing.T) {
	ring := agent.NewKeyring()
	entered := make(chan struct{})
	endpoint := start(t, ring, unlockFunc(func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }), "")
	c, conn := client(t, endpoint)
	done := make(chan struct{})
	go func() { defer close(done); _, _ = c.List() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("unlock did not start")
	}
	// Cleanup from start() cancels the server while its unlock is pending.
	conn.Close()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("client did not disconnect")
		}
	})
}
