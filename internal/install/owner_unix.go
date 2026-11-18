//go:build !windows

package install

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"keepassxc-agent-middleware/internal/transport"
)

type processIdentity struct {
	uid    uint32
	birth  string
	zombie bool
}

type unixProcess struct {
	pid        int
	image      string
	identity   processIdentity
	fd         int
	connection net.Conn
}

func (p *unixProcess) close() {
	if p.connection != nil {
		p.connection.Close()
		p.connection = nil
	}
	if p.fd >= 0 {
		unix.Close(p.fd)
		p.fd = -1
	}
}

func unixEndpointOwner(ctx context.Context, p *Plan, endpoint string) (*unixProcess, error) {
	probe, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := transport.Dial(probe, endpoint)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect proxy socket %s: %w", endpoint, err)
	}
	owner := &unixProcess{fd: -1, connection: conn}
	reject := func(err error) (*unixProcess, error) {
		owner.close()
		return nil, fmt.Errorf("socket %s belongs to PID %d (%s); refusing automatic control: %w", endpoint, owner.pid, owner.image, err)
	}
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return reject(err)
	}
	var uid uint32
	var peerErr error
	if err := raw.Control(func(fd uintptr) { owner.pid, uid, owner.fd, peerErr = peerProcess(int(fd)) }); err != nil {
		return reject(err)
	}
	if peerErr != nil {
		return reject(peerErr)
	}
	if uid != uint32(os.Getuid()) || owner.pid == os.Getpid() {
		return reject(errors.New("socket is not owned by another process of the current user"))
	}
	owner.identity, err = unixIdentity(owner.pid)
	if err != nil {
		return reject(err)
	}
	if owner.identity.uid != uid || owner.identity.zombie {
		return reject(errors.New("socket process credentials changed or process exited"))
	}
	if err := pinUnixProcess(owner); err != nil {
		return reject(err)
	}
	owner.image, err = unixExecutable(ctx, owner.pid)
	if err != nil {
		return reject(err)
	}
	if err := trustedImage(p, owner.image); err != nil {
		return reject(err)
	}
	if alive, err := owner.alive(); err != nil || !alive {
		if err == nil {
			err = errors.New("process exited during identity verification")
		}
		return reject(err)
	}
	return owner, nil
}

func (p *unixProcess) alive() (bool, error) {
	identity, err := unixIdentity(p.pid)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !identity.zombie && identity == p.identity, nil
}

func (p *unixProcess) wait(ctx context.Context) error {
	for {
		alive, err := p.alive()
		if err != nil {
			return err
		}
		if !alive {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (p *unixProcess) signal(signal unix.Signal) error {
	alive, err := p.alive()
	if err != nil || !alive {
		return err
	}
	err = signalUnixProcess(p, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (p *unixProcess) stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.signal(unix.SIGTERM); err != nil {
		return err
	}
	grace, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := p.wait(grace)
	cancel()
	if err == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.signal(unix.SIGKILL); err != nil {
		return err
	}
	return p.wait(ctx)
}

func verifyStartedProxy(ctx context.Context, p *Plan) error {
	owner, err := unixEndpointOwner(ctx, p, p.Config.Listen)
	if err != nil {
		return err
	}
	if owner == nil {
		return errors.New("proxy exited before startup completed")
	}
	defer owner.close()
	actual, err := os.Stat(owner.image)
	if err != nil {
		return err
	}
	expected, err := os.Stat(p.Binary)
	if err != nil {
		return err
	}
	if !os.SameFile(actual, expected) {
		return fmt.Errorf("socket is served by another installed version, PID %d: %s", owner.pid, owner.image)
	}
	return nil
}
