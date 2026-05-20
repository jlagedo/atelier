//go:build linux

package vsock

import (
	"net"

	"github.com/mdlayher/vsock"
)

// Listen binds AF_VSOCK on GuestRPCPort (any CID) and returns a net.Listener
// ready for rpc.Server.Serve. Runs in the guest, where the matched kernel's
// hv_sock transport backs AF_VSOCK.
func Listen() (net.Listener, error) {
	return vsock.Listen(GuestRPCPort, nil)
}
