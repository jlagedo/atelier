//go:build !windows

package rpc

import (
	"net"
	"os"
)

// DefaultAddress is a unix socket used for terminal-driven development on
// non-Windows hosts. On Windows the transport is a named pipe (design.md §8).
const DefaultAddress = "/tmp/atelier-host.sock"

// Listen opens the unix-socket listener, clearing any stale socket file first.
func Listen(addr string) (net.Listener, error) {
	_ = os.Remove(addr)
	return net.Listen("unix", addr)
}

// Dial connects to the unix socket.
func Dial(addr string) (net.Conn, error) {
	return net.Dial("unix", addr)
}
