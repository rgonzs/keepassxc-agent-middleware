package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func peerProcess(fd int) (int, uint32, int, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, 0, -1, err
	}
	// New kernels can return a handle to the exact socket peer atomically.
	pidfd, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_PEERPIDFD)
	if err != nil {
		if errors.Is(err, unix.ENOPROTOOPT) || errors.Is(err, unix.EINVAL) {
			return int(cred.Pid), cred.Uid, -1, nil
		}
		return int(cred.Pid), cred.Uid, -1, err
	}
	if pidfd >= 0 {
		unix.CloseOnExec(pidfd)
	}
	return int(cred.Pid), cred.Uid, pidfd, err
}

func unixIdentity(pid int) (processIdentity, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	info, err := os.Stat(dir)
	if err != nil {
		return processIdentity{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return processIdentity{}, err
	}
	// comm is parenthesized and may contain spaces or parentheses.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return processIdentity{}, errors.New("invalid process stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return processIdentity{}, errors.New("incomplete process stat")
	}
	return processIdentity{uid: info.Sys().(*syscall.Stat_t).Uid, birth: fields[19], zombie: fields[0] == "Z" || fields[0] == "X"}, nil
}

func unixExecutable(_ context.Context, pid int) (string, error) {
	return os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
}

func pinUnixProcess(p *unixProcess) error {
	if p.fd < 0 {
		fd, err := unix.PidfdOpen(p.pid, 0)
		if err != nil {
			return fmt.Errorf("pin process identity using pidfd (Linux 5.3+ required): %w", err)
		}
		p.fd = fd
	}
	return nil
}

func signalUnixProcess(p *unixProcess, signal unix.Signal) error {
	return unix.PidfdSendSignal(p.fd, signal, nil, 0)
}
