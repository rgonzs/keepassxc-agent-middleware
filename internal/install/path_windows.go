package install

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"syscall"
	"unsafe"
)

func readUserPath() (pathValue, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return pathValue{}, nil
	}
	if err != nil {
		return pathValue{}, err
	}
	defer key.Close()
	value, kind, err := key.GetStringValue("Path")
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return pathValue{}, nil
	}
	if err != nil {
		return pathValue{}, err
	}
	if kind != registry.SZ && kind != registry.EXPAND_SZ {
		return pathValue{}, fmt.Errorf("unsupported user PATH registry type %d", kind)
	}
	return pathValue{value, kind, true}, nil
}
func writeUserPath(v pathValue) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !v.Exists {
		err = key.DeleteValue("Path")
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			err = nil
		}
	} else if v.Type == registry.EXPAND_SZ {
		err = key.SetExpandStringValue("Path", v.Value)
	} else {
		err = key.SetStringValue("Path", v.Value)
	}
	if err != nil {
		return err
	}
	// Inform desktop applications; existing terminals retain their environment.
	topic, _ := windows.UTF16PtrFromString("Environment")
	var result uintptr
	windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW").Call(0xffff, 0x001a, 0, uintptr(unsafe.Pointer(topic)), 2, 1000, uintptr(unsafe.Pointer(&result)))
	return nil
}
