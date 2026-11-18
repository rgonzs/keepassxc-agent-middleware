//go:build !windows

package install

import (
	"context"
	"errors"
)

func stopWindows(context.Context, *Plan, bool) error {
	return errors.New("Windows task control is unavailable on this platform")
}
func startWindowsProxy(context.Context, *Plan) error {
	return errors.New("Windows task control is unavailable on this platform")
}
