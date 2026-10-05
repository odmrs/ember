//go:build !windows

package connector

import (
	"errors"
	"syscall"
)

// refused reports whether the host answered but nothing listens on the port.
func refused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
