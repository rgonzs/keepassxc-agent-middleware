// Package lifecycle provides a cooperative stop request for background processes.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func eventName(pid uint32) (*uint16, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.UTF16PtrFromString(fmt.Sprintf(`Local\KeePassXCAgentStop-%s-%d`, user.User.Sid.String(), pid))
}

// Context cancels when this process receives its private stop event. The event
// is restricted to the current Windows user and never carries SSH messages.
func Context(parent context.Context) (context.Context, func(), error) {
	name, err := eventName(uint32(os.Getpid()))
	if err != nil {
		return nil, nil, err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, nil, err
	}
	attrs := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	event, err := windows.CreateEvent(&attrs, 1, 0, name)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			state, err := windows.WaitForSingleObject(event, 100)
			if err != nil || state == windows.WAIT_OBJECT_0 {
				cancel()
				return
			}
		}
	}()
	return ctx, func() { cancel(); <-done; windows.CloseHandle(event) }, nil
}

// RequestStop returns false for older versions that have no cooperative event.
func RequestStop(pid uint32) (bool, error) {
	name, err := eventName(pid)
	if err != nil {
		return false, err
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(event)
	return true, windows.SetEvent(event)
}
