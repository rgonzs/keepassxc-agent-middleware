package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"keepassxc-agent-middleware/internal/transport"
)

type Unlocker interface{ Unlock(context.Context) error }

type Server struct {
	Upstream    string
	Fingerprint string
	Unlocker    Unlocker
	Timeout     time.Duration
	IOTimeout   time.Duration
	IdleTimeout time.Duration
	Logger      *slog.Logger
	gate        chan struct{}
	retryAfter  time.Time // Protected by gate; suppress prompt storms after cancellation.
}

// Serve preserves one upstream connection per client, including extension state
// such as OpenSSH session-bind. A disappearing upstream never replays signatures.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	if s.Timeout <= 0 {
		s.Timeout = 60 * time.Second
	}
	if s.IOTimeout <= 0 {
		s.IOTimeout = 5 * time.Second
	}
	if s.IdleTimeout <= 0 {
		s.IdleTimeout = 5 * time.Minute
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	s.gate = make(chan struct{}, 1)
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	var workers sync.WaitGroup
	defer func() { cancel(); listener.Close(); workers.Wait() }()
	slots := make(chan struct{}, 64)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-slots }()
				s.handle(ctx, conn)
			}()
		default:
			conn.Close()
		}
	}
}

func (s *Server) roundTrip(ctx context.Context, upstream net.Conn, request []byte) ([]byte, error) {
	deadline := time.Now().Add(s.IOTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = upstream.SetDeadline(deadline)
	if err := writePacket(upstream, request); err != nil {
		return nil, err
	}
	return readPacket(upstream)
}

func (s *Server) handle(ctx context.Context, client net.Conn) {
	defer client.Close()
	stopClient := context.AfterFunc(ctx, func() { client.Close() })
	defer stopClient()
	dialCtx, cancel := context.WithTimeout(ctx, s.IOTimeout)
	upstream, err := transport.Dial(dialCtx, s.Upstream)
	cancel()
	if err != nil {
		s.Logger.Warn("SSH agent unavailable", "error", err)
		_ = client.SetWriteDeadline(time.Now().Add(s.IOTimeout))
		_ = writePacket(client, []byte{failure})
		return
	}
	defer upstream.Close()
	stopUpstream := context.AfterFunc(ctx, func() { upstream.Close() })
	defer stopUpstream()
	for {
		_ = client.SetReadDeadline(time.Now().Add(s.IdleTimeout))
		request, err := readPacket(client)
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				s.Logger.Debug("client disconnected", "error", err)
			}
			return
		}
		response, err := s.roundTrip(ctx, upstream, request)
		if err != nil {
			return
		}
		switch request[0] {
		case identitiesRequest:
			keys, parseErr := identities(response)
			if parseErr == nil && !s.ready(keys, nil) {
				if updated, unlockErr := s.ensureKeys(ctx, upstream, nil); unlockErr == nil {
					response = updated
				} else {
					s.Logger.Warn("key unavailable after unlock request", "error", unlockErr)
				}
			}
		case signRequest:
			if response[0] == failure {
				key, _, parseErr := takeString(request[1:])
				if parseErr == nil {
					// A refused signature for an existing key (confirmation, policy, locked
					// agent) must NOT become an unrelated vault unlock prompt.
					listed, listErr := s.roundTrip(ctx, upstream, []byte{identitiesRequest})
					keys, parseErr := identities(listed)
					if listErr != nil {
						return
					}
					if parseErr == nil && !contains(keys, key) {
						if _, unlockErr := s.ensureKeys(ctx, upstream, key); unlockErr == nil {
							response, err = s.roundTrip(ctx, upstream, request)
							if err != nil {
								return
							}
						} else {
							s.Logger.Warn("signing key unavailable", "error", unlockErr)
						}
					}
				}
			}
		}
		_ = client.SetWriteDeadline(time.Now().Add(s.IOTimeout))
		if err := writePacket(client, response); err != nil {
			return
		}
	}
}

func contains(keys [][]byte, key []byte) bool {
	for _, candidate := range keys {
		if bytes.Equal(candidate, key) {
			return true
		}
	}
	return false
}
func (s *Server) ready(keys [][]byte, requested []byte) bool {
	if requested != nil {
		return contains(keys, requested)
	}
	if s.Fingerprint == "" {
		return len(keys) > 0
	}
	for _, key := range keys {
		if fingerprint(key) == s.Fingerprint {
			return true
		}
	}
	return false
}

func (s *Server) ensureKeys(parent context.Context, upstream net.Conn, requested []byte) (result []byte, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, s.Timeout)
	defer cancel()
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.gate }()
	query := func() ([]byte, bool, error) {
		reply, err := s.roundTrip(ctx, upstream, []byte{identitiesRequest})
		if err != nil {
			return nil, false, err
		}
		keys, err := identities(reply)
		return reply, s.ready(keys, requested), err
	}
	// Another client may have completed the same unlock while we were waiting.
	reply, ready, err := query()
	if err != nil || ready {
		return reply, err
	}
	if time.Now().Before(s.retryAfter) {
		return nil, errors.New("unlock retry cooldown (5 seconds)")
	}
	defer func() {
		if resultErr != nil {
			s.retryAfter = time.Now().Add(5 * time.Second)
		}
	}()
	s.Logger.Info("requesting KeePassXC unlock")
	if err := s.Unlocker.Unlock(ctx); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		reply, ready, err = query()
		if err != nil {
			return nil, err
		}
		if ready {
			s.Logger.Info("SSH key available")
			return reply, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("database unlocked but expected key not loaded: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// Probe is read-only and never invokes KeePassXC unlock.
func Probe(ctx context.Context, endpoint string) (int, error) {
	conn, err := transport.Dial(ctx, endpoint)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	}
	if err = writePacket(conn, []byte{identitiesRequest}); err != nil {
		return 0, err
	}
	reply, err := readPacket(conn)
	if err != nil {
		return 0, err
	}
	keys, err := identities(reply)
	return len(keys), err
}
