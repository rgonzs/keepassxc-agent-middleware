//go:build !windows

package install

import "errors"

// Enables cross-platform planning tests; actual registry mutation is only
// requested by SystemManager on Windows.
func readUserPath() (pathValue, error) { return pathValue{}, nil }
func writeUserPath(pathValue) error    { return errors.New("Windows user PATH unavailable") }
