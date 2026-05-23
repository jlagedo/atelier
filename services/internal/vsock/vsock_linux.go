//go:build linux

package vsock

import (
	"net"

	"github.com/mdlayher/vsock"
)

// Listen binds AF_VSOCK on GuestRPCPort (any CID) and returns a net.Listener
// ready for rpc.Server.Serve. Runs in the guest, where AF_VSOCK is backed by a
// hypervisor-specific transport — hv_sock under Hyper-V, virtio-vsock under Apple's
// Virtualization.framework — loaded at boot by image/guest/init.sh.
func Listen() (net.Listener, error) {
	return vsock.Listen(GuestRPCPort, nil)
}
