package install

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func peerProcess(fd int) (int, uint32, int, error) {
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil {
		return 0, 0, -1, err
	}
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, 0, -1, err
	}
	return pid, cred.Uid, -1, nil
}

func unixIdentity(pid int) (processIdentity, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if errors.Is(err, unix.EIO) {
		return processIdentity{}, unix.ESRCH
	} // No record was returned for an exited PID.
	if err != nil {
		return processIdentity{}, err
	}
	if info.Proc.P_starttime.Sec == 0 {
		return processIdentity{}, errors.New("kernel process birth time is unavailable")
	}
	return processIdentity{
		uid:    info.Eproc.Ucred.Uid,
		birth:  fmt.Sprintf("%d:%d", info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec),
		zombie: info.Proc.P_stat == 5, // SZOMB, from sys/proc.h
	}, nil
}

func unixExecutable(ctx context.Context, pid int) (string, error) {
	// macOS's system lsof exposes the actual program text vnode. Do not infer
	// identity from argv/ps command strings, which the process can change.
	output, err := command(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-F0n")
	if err != nil {
		return "", err
	}
	for _, field := range strings.Split(output, "\x00") {
		field = strings.TrimLeft(field, "\n") // lsof separates records with a newline.
		if strings.HasPrefix(field, "n/") {
			return field[1:], nil
		}
	}
	return "", errors.New("cannot identify executable program text from lsof")
}

func pinUnixProcess(p *unixProcess) error { return nil }

func signalUnixProcess(p *unixProcess, signal unix.Signal) error {
	// alive() rechecks the kernel birth time immediately before each signal;
	// a PID file or a matching process name never authorizes termination.
	return unix.Kill(p.pid, signal)
}
