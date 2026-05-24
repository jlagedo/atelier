// Package vmm orchestrates utility VMs above a platform driver. The broker owns
// a Manager and exposes it through gated RPC methods; the client never sees
// platform details like HCS documents, hvsocket GUIDs, or host share resources.
package vmm

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/jlagedo/atelier/services/internal/netjail"
)

// ErrUnsupported is returned by the non-platform stub when no VMM backend exists.
var ErrUnsupported = errors.New("vmm: utility VMs are unsupported on this platform")

// VMConfig is the host-facing description of a VM. KernelPath/RootFSPath are
// host file paths to a direct-boot kernel and our ext4 rootfs VHD. Keep this
// shape stable for the broker protocol; platform-specific drivers translate it
// into their native configuration.
type VMConfig struct {
	ID         string
	KernelPath string
	// InitrdPath is the host path to the matched boot initramfs. Optional: empty
	// keeps the older built-in-driver boot path.
	InitrdPath string
	RootFSPath string
	// GuestdImagePath is the host path to the guestd volume — its own ro image holding
	// /guestd, attached as a second disk (VZ -> /dev/vdb; HCS -> a SCSI disk) and mounted
	// by init.sh (LABEL=guestd). guestd is not baked into the rootfs, so this is the sole
	// delivery path; the desktop/vmctl always set it. Empty = no second disk (boot to shell).
	GuestdImagePath string
	MemoryMB        uint64
	CPUCount        int32
}

// WorkspaceShare is the platform-neutral description of a host folder exposed
// to the guest. Windows maps this to a Plan9/9p share; future platforms can map
// it to their own file-sharing device.
type WorkspaceShare struct {
	HostPath string
	ReadOnly bool
	Tag      string
	Port     uint32
}

// Driver is the platform seam below the broker. It speaks Atelier concepts; the
// implementation owns the OS-specific VM API, guest socket transport, file-share
// mechanism, and egress link setup.
type Driver interface {
	Create(ctx context.Context, cfg VMConfig) error
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	DialGuest(ctx context.Context, id string, port uint32) (net.Conn, error)
	AttachWorkspace(ctx context.Context, id string, share WorkspaceShare) error
	DetachWorkspace(ctx context.Context, id string, share WorkspaceShare) error
	StartEgress(ctx context.Context, id string, filter *netjail.Allowlist) (io.Closer, error)
}
