//go:build !windows

package tray

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"keepassxc-agent-middleware/internal/settings"
	"keepassxc-agent-middleware/internal/transport"
)

const ControlGreeting = "keepassxc-agent-tray-v1"

// The private control socket proves the process role to the installer. A
// matching executable/PID alone cannot distinguish the tray from the SSH proxy.
func unixRuntime(h Handlers, c *Controller) (context.Context, func(), bool, error) {
	root := h.RuntimeRoot
	if root == "" {
		root = settings.Root()
	}
	endpoint := filepath.Join(root, "tray.sock")
	if conn, err := net.DialTimeout("unix", endpoint, time.Second); err == nil {
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		fmt.Fprintln(conn, "status")
		greeting, _ := bufio.NewReader(conn).ReadString('\n')
		if greeting == ControlGreeting+"\n" {
			return nil, func() {}, true, nil
		}
		return nil, nil, false, errors.New("tray control endpoint is occupied by another service")
	}
	listener, err := transport.Listen(endpoint)
	if err != nil {
		return nil, nil, false, err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				line, _ := bufio.NewReader(conn).ReadString('\n')
				if line == "status\n" {
					fmt.Fprintln(conn, ControlGreeting)
				}
			}(conn)
		}
	}()
	return ctx, func() { cancel(); stop(); listener.Close() }, false, nil
}
