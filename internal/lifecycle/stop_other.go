//go:build !windows

package lifecycle

import "context"

func Context(parent context.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(parent)
	return ctx, cancel, nil
}
