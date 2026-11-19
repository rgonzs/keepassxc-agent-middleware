// Package localfile opens regular files without following a link at the final path component.
package localfile

import (
	"errors"
	"io"
	"os"
)

var errNotRegular = errors.New("expected a regular file without symbolic links")

func Open(path string) (*os.File, os.FileInfo, error) {
	return open(path, false)
}

func OpenAppend(path string) (*os.File, error) {
	file, _, err := open(path, true)
	return file, err
}

// ReadConfig checks the ownership and write permissions of the opened file
// before trusting its contents.
func ReadConfig(path string) ([]byte, error) {
	file, info, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := checkConfigAccess(file, info); err != nil {
		return nil, &os.PathError{Op: "check configuration permissions", Path: path, Err: err}
	}
	return io.ReadAll(file)
}

func open(path string, appendLog bool) (*os.File, os.FileInfo, error) {
	// Reject known special files before opening them. This check alone would
	// race with a replacement, so openFile also refuses to follow the final link.
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: errNotRegular}
	}
	if err != nil && (!appendLog || !errors.Is(err, os.ErrNotExist)) {
		return nil, nil, err
	}
	file, err := openFile(path, appendLog)
	if err != nil {
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	info, err = file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = &os.PathError{Op: "stat", Path: path, Err: errNotRegular}
	}
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}
