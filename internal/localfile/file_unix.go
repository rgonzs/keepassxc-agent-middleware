//go:build !windows

package localfile

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func openFile(path string, appendLog bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	if appendLog {
		flags |= unix.O_WRONLY | unix.O_CREAT | unix.O_APPEND
	}
	fd, err := unix.Open(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func checkConfigAccess(file *os.File, info os.FileInfo) error {
	if info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return errors.New("configuration must be owned by the current user")
	}
	if info.Mode().Perm()&0022 != 0 {
		return errors.New("configuration must not be writable by group or others")
	}
	return checkExtendedACL(file)
}
