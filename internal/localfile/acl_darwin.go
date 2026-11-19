package localfile

import (
	"encoding/binary"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func checkExtendedACL(file *os.File) error {
	attrs := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	// The result starts with the length and an attrreference (offset, length).
	// Only the reference is needed to detect the presence of an extended ACL.
	var result [12]byte
	_, _, err := unix.Syscall6(unix.SYS_FGETATTRLIST, file.Fd(), uintptr(unsafe.Pointer(&attrs)),
		uintptr(unsafe.Pointer(&result)), uintptr(len(result)), unix.FSOPT_REPORT_FULLSIZE, 0)
	if err != 0 {
		return err
	}
	if binary.NativeEndian.Uint32(result[8:]) != 0 {
		return errors.New("configuration must not have a macOS extended ACL")
	}
	return nil
}
