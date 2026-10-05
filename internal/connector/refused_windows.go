package connector

import (
	"errors"
	"syscall"
)

// wsaeconnrefused is Winsock's "connection refused"; syscall.ECONNREFUSED
// on Windows is an invented value the network stack never returns.
const wsaeconnrefused syscall.Errno = 10061

// refused reports whether the host answered but nothing listens on the port.
func refused(err error) bool {
	return errors.Is(err, wsaeconnrefused) || errors.Is(err, syscall.ECONNREFUSED)
}
