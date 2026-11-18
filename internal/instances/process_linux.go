package instances

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func socketPeer(fd int) (int, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	if cred.Uid != uint32(os.Getuid()) {
		return 0, errors.New("socket belongs to another user")
	}
	return int(cred.Pid), nil
}
func processIdentity(pid int) (string, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return "", errors.New("process belongs to another user")
	}
	data, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return "", err
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", errors.New("invalid process stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return "", errors.New("process exited or stat is incomplete")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19], nil
}
func processImage(_ context.Context, pid int) (string, error) {
	return os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
}
