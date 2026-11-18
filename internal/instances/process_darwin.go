package instances

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func socketPeer(fd int) (int, error) {
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil {
		return 0, err
	}
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, err
	}
	if cred.Uid != uint32(os.Getuid()) {
		return 0, errors.New("socket belongs to another user")
	}
	return pid, nil
}
func processIdentity(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if info.Eproc.Ucred.Uid != uint32(os.Getuid()) || info.Proc.P_stat == 5 || info.Proc.P_starttime.Sec == 0 {
		return "", errors.New("process exited or belongs to another user")
	}
	return fmt.Sprintf("%d:%d", info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec), nil
}
func processImage(ctx context.Context, pid int) (string, error) {
	data, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-F0n").Output()
	if err != nil {
		return "", err
	}
	for _, field := range strings.Split(string(data), "\x00") {
		field = strings.TrimLeft(field, "\n")
		if strings.HasPrefix(field, "n/") {
			return field[1:], nil
		}
	}
	return "", errors.New("cannot identify process executable")
}
