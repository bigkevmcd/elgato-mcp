//go:build unix

package elgato

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reusePort allows the mDNS port to be shared with other mDNS
// implementations, such as avahi.
func reusePort(_, _ string, c syscall.RawConn) error {
	var err error

	ctrlErr := c.Control(func(fd uintptr) {
		err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if err == nil {
			err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}
	})
	if ctrlErr != nil {
		return ctrlErr
	}

	return err
}
