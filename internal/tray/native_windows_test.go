package tray

import (
	"testing"
	"unsafe"
)

func TestNativeLayoutsAndIcons(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		for name, sizes := range map[string][2]uintptr{
			"NOTIFYICONDATAW": {unsafe.Sizeof(iconData{}), 976},
			"WNDCLASSEXW":     {unsafe.Sizeof(windowClass{}), 80},
			"MSG":             {unsafe.Sizeof(message{}), 48},
		} {
			if sizes[0] != sizes[1] {
				t.Fatalf("%s: size %d, want %d", name, sizes[0], sizes[1])
			}
		}
	}
	for _, state := range []Snapshot{{}, {Proxy: true}, {Proxy: true, Healthy: true}} {
		data := iconDIB(state)
		icon, _, err := createIcon.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, 0x30000, 32, 32, 0)
		if icon == 0 {
			t.Fatalf("Windows rejected icon: %v", err)
		}
		destroyIcon.Call(icon)
	}
}
