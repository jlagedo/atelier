//go:build windows

package rpc

import (
	"net"

	winio "github.com/Microsoft/go-winio"
)

// DefaultAddress is the broker's named pipe (design.md §8 — Hop 2).
// The shipping service further restricts access with a security group (design.md §9).
const DefaultAddress = `\\.\pipe\atelier-host`

// Listen opens the named-pipe listener.
func Listen(addr string) (net.Listener, error) {
	return winio.ListenPipe(addr, &winio.PipeConfig{MessageMode: false})
}

// Dial connects to the named pipe.
func Dial(addr string) (net.Conn, error) {
	return winio.DialPipe(addr, nil)
}
