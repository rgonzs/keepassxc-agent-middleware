package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"keepassxc-agent-middleware/internal/transport"
)

// Control operates the owned proxy while preserving its login configuration.
// Stop removes service
// registrations only during installation/removal.
func (SystemManager) Control(ctx context.Context, p *Plan, action string) error {
	if action != "start" && action != "stop" && action != "restart" {
		return fmt.Errorf("unknown task action %q", action)
	}
	unlock, err := lockRoot(p.Root)
	if err != nil {
		return err
	}
	defer unlock()
	if p.OS != "windows" {
		if action == "stop" || action == "restart" {
			if err := stopUnix(ctx, p, false, command); err != nil {
				return err
			}
		}
		if action == "start" || action == "restart" {
			return startUnixProxy(ctx, p, command)
		}
		return nil
	}
	if action == "stop" || action == "restart" {
		if err := stopWindows(ctx, p, false); err != nil {
			return err
		}
	}
	if action == "start" || action == "restart" {
		return startWindowsProxy(ctx, p)
	}
	return nil
}

func waitEndpoint(parent context.Context, endpoint string, reachable bool) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		probe, stop := context.WithTimeout(ctx, 250*time.Millisecond)
		conn, err := transport.Dial(probe, endpoint)
		stop()
		if err == nil {
			conn.Close()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		absent := errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
		if err != nil && !absent && !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("cannot determine proxy state at %s: %w", endpoint, err)
		}
		if (reachable && err == nil) || (!reachable && absent) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("proxy did not reach requested state (reachable=%t): %w", reachable, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
