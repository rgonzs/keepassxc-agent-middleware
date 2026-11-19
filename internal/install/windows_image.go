package install

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
)

// windowsBackgroundImage produces the task's GUI-subsystem executable from
// the console CLI. The original CLI retains its subsystem and all its bytes.
func windowsBackgroundImage(image []byte) ([]byte, error) {
	const (
		dosPEOffset = 0x3c
		coffSize    = 20
		subsystem   = 68 // Same offset in PE32 and PE32+ optional headers.
		checksum    = 64
	)
	invalid := errors.New("invalid Windows PE executable")
	le := binary.LittleEndian
	if len(image) < dosPEOffset+4 || string(image[:2]) != "MZ" {
		return nil, invalid
	}
	peOffset := int64(le.Uint32(image[dosPEOffset:]))
	optional := peOffset + 4 + coffSize
	if optional > int64(len(image)) || string(image[peOffset:peOffset+4]) != "PE\x00\x00" {
		return nil, invalid
	}
	size := int64(le.Uint16(image[optional-4:]))
	if size < subsystem+2 || optional+size > int64(len(image)) {
		return nil, invalid
	}
	header := image[optional : optional+size]
	var directories int
	switch le.Uint16(header) {
	case 0x10b: // PE32
		directories = 96
	case 0x20b: // PE32+
		directories = 112
	default:
		return nil, invalid
	}
	if len(header) < directories {
		return nil, invalid
	}
	switch le.Uint16(header[subsystem:]) {
	case pe.IMAGE_SUBSYSTEM_WINDOWS_GUI:
		return image, nil
	case pe.IMAGE_SUBSYSTEM_WINDOWS_CUI:
	default:
		return nil, errors.New("unsupported Windows executable subsystem")
	}
	if le.Uint32(header[directories-4:]) > pe.IMAGE_DIRECTORY_ENTRY_SECURITY {
		entry := directories + 8*pe.IMAGE_DIRECTORY_ENTRY_SECURITY
		if len(header) < entry+8 {
			return nil, invalid
		}
		if le.Uint64(header[entry:]) != 0 {
			return nil, errors.New("cannot convert a signed console executable; use an unsigned CLI or a Windows GUI-subsystem build")
		}
	}
	background := bytes.Clone(image)
	le.PutUint32(background[optional+checksum:], 0)
	le.PutUint16(background[optional+subsystem:], pe.IMAGE_SUBSYSTEM_WINDOWS_GUI)
	return background, nil
}
