package instances

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"net"
	"os"
	"strings"
)

func privateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid instance directory")
	}
	return nil
}
func localEndpoint(endpoint string) bool {
	return strings.HasPrefix(strings.ToLower(strings.ReplaceAll(endpoint, "/", `\`)), `\\.\pipe\`)
}
func peerPID(conn net.Conn) (int, error) {
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0, errors.New("pipe has no server handle")
	}
	var pid uint32
	err := windows.GetNamedPipeServerProcessId(windows.Handle(file.Fd()), &pid)
	return int(pid), err
}
func inspect(pid int) (windows.Handle, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	fail := func(err error) (windows.Handle, error) { windows.CloseHandle(handle); return 0, err }
	state, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return fail(err)
	}
	if state != uint32(windows.WAIT_TIMEOUT) {
		return fail(errors.New("process exited"))
	}
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		return fail(err)
	}
	user, err := token.GetTokenUser()
	token.Close()
	if err != nil {
		return fail(err)
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fail(err)
	}
	if !user.User.Sid.Equals(current.User.Sid) {
		return fail(errors.New("process belongs to another user"))
	}
	return handle, nil
}
func processIdentity(pid int) (string, error) {
	handle, err := inspect(pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", creation.HighDateTime, creation.LowDateTime), nil
}
func processImage(_ context.Context, pid int) (string, error) {
	handle, err := inspect(pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}
