//go:build !windows

package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

func Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
}

// Listen holds an advisory lock for its lifetime. A socket left by a crashed
// proxy is removed only if it is owned by this user and refuses connections.
func Listen(endpoint string) (net.Listener, error) {
	parent := filepath.Dir(endpoint)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || st.Uid != uint32(os.Getuid()) {
		return nil, fmt.Errorf("socket directory must be owned by you with mode 0700: %s", parent)
	}
	lockPath := endpoint + ".lock"
	if info, err := os.Lstat(lockPath); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing non-regular socket lock: %s", lockPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	release := func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); _ = file.Close() }
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another proxy owns this socket: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			release()
		}
	}()
	if info, err := os.Lstat(endpoint); err == nil {
		st, ok := info.Sys().(*syscall.Stat_t)
		if info.Mode()&os.ModeSocket == 0 || !ok || st.Uid != uint32(os.Getuid()) {
			return nil, fmt.Errorf("refusing to remove non-owned socket: %s", endpoint)
		}
		conn, err := net.DialTimeout("unix", endpoint, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil, fmt.Errorf("socket is already active: %s", endpoint)
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("cannot prove socket is stale: %w", err)
		}
		current, err := os.Lstat(endpoint)
		if err != nil {
			return nil, err
		}
		if !os.SameFile(info, current) {
			return nil, errors.New("socket changed during stale check")
		}
		if err := os.Remove(endpoint); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(endpoint, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	failed = false
	return &lockedListener{Listener: listener, release: release}, nil
}

func DefaultListen() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "keepassxc-agent-"+strconv.Itoa(os.Getuid()), "agent.sock")
}

func DefaultUpstream() string { return os.Getenv("SSH_AUTH_SOCK") }

func SameEndpoint(a, b string) bool {
	canonical := func(s string) string {
		if resolved, err := filepath.EvalSymlinks(s); err == nil {
			s = resolved
		}
		abs, _ := filepath.Abs(s)
		return abs
	}
	return canonical(a) == canonical(b)
}

type lockedListener struct {
	net.Listener
	release func()
	once    sync.Once
	err     error
}

func (l *lockedListener) Close() error {
	l.once.Do(func() { l.err = l.Listener.Close(); l.release() })
	return l.err
}
