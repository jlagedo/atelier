//go:build !linux

package vsock

import (
	"errors"
	"net"
)

// Listen is a stub so the tree builds on non-Linux hosts (e.g. the Windows dev
// box running `go build ./...`). guestd only ever runs in the Linux guest.
func Listen() (net.Listener, error) {
	return nil, errors.New("vsock listen: linux only")
}
