//go:build !unix

package elgato

import "syscall"

// reusePort is a no-op on platforms without SO_REUSEPORT. Binding the mDNS
// port fails if it's already in use and discovery falls back to the
// ephemeral port.
func reusePort(_, _ string, _ syscall.RawConn) error {
	return nil
}
