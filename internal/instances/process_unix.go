//go:build !windows

package instances

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func privateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || st.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("instance directory must be private and owned by you: %s", path)
	}
	return nil
}
func localEndpoint(endpoint string) bool { return filepath.IsAbs(endpoint) }
func peerPID(conn net.Conn) (int, error) {
	socket, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a Unix socket")
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var peerErr error
	if err := raw.Control(func(fd uintptr) { pid, peerErr = socketPeer(int(fd)) }); err != nil {
		return 0, err
	}
	return pid, peerErr
}
