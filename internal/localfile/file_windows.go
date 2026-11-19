package localfile

import (
	"errors"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openFile(path string, appendLog bool) (*os.File, error) {
	fullPath, err := windows.FullPath(path)
	if err != nil {
		return nil, err
	}
	// Preserve long paths when opening directly with CreateFile.
	if len(fullPath) >= 248 && !strings.HasPrefix(fullPath, `\\?\`) && !strings.HasPrefix(fullPath, `\\.\`) {
		if strings.HasPrefix(fullPath, `\\`) {
			fullPath = `\\?\UNC\` + fullPath[2:]
		} else {
			fullPath = `\\?\` + fullPath
		}
	}
	name, err := windows.UTF16PtrFromString(fullPath)
	if err != nil {
		return nil, err
	}
	access, disposition := uint32(windows.GENERIC_READ), uint32(windows.OPEN_EXISTING)
	if appendLog {
		access = windows.FILE_APPEND_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
		disposition = windows.OPEN_ALWAYS
	}
	handle, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	kind, err := windows.GetFileType(handle)
	if err == nil && kind != windows.FILE_TYPE_DISK {
		err = errNotRegular
	}
	if err == nil {
		var info windows.ByHandleFileInformation
		err = windows.GetFileInformationByHandle(handle, &info)
		if err == nil && info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
			err = errNotRegular
		}
	}
	if err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func checkConfigAccess(file *os.File, _ os.FileInfo) error {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if owner == nil || !owner.Equals(tokenUser.User.Sid) {
		return errors.New("configuration must be owned by the current user")
	}
	return checkConfigDACL(sd, owner)
}

func checkConfigDACL(sd *windows.SECURITY_DESCRIPTOR, owner *windows.SID) error {
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return errors.New("configuration must have a restricted Windows ACL")
	}
	const writeRights = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_ATTRIBUTES |
		windows.FILE_WRITE_EA | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER |
		windows.GENERIC_WRITE | windows.GENERIC_ALL
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		// Ordinary file ACL entries use a SID directly after the access mask.
		// Reject unrecognized entries to avoid accepting an unsupported grant.
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("configuration has an unsupported Windows ACL entry")
		}
		if ace.Mask&writeRights == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(owner) && !sid.IsWellKnown(windows.WinCreatorOwnerRightsSid) &&
			!sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return errors.New("configuration grants write access to an untrusted Windows principal")
		}
	}
	return nil
}
