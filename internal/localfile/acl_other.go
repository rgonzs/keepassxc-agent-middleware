//go:build !darwin && !windows

package localfile

import "os"

// On Linux the group mode bits include the POSIX ACL mask.
func checkExtendedACL(_ *os.File) error { return nil }
