//go:build darwin

package vmm

import (
	"context"
	"io"
	"log/slog"
	"net"

	"github.com/jlagedo/atelier/services/internal/netjail"
)

// darwinDriver will map the platform-neutral VMM seam onto Apple's
// Virtualization.framework (Code-Hex/vz). It is a stub today (every method
// returns ErrUnsupported); S4 onward fills it in. Keeping it a named struct
// here lets later slices add fields/methods without churn.
type darwinDriver struct {
	log *slog.Logger
}

// NewDriver returns the macOS Virtualization.framework VMM driver.
func NewDriver(log *slog.Logger) Driver {
	if log == nil {
		log = slog.Default()
	}
	return &darwinDriver{log: log}
}

func (*darwinDriver) Create(context.Context, VMConfig) error { return ErrUnsupported }
func (*darwinDriver) Start(context.Context, string) error    { return ErrUnsupported }
func (*darwinDriver) Stop(context.Context, string) error     { return ErrUnsupported }
func (*darwinDriver) DialGuest(context.Context, string, uint32) (net.Conn, error) {
	return nil, ErrUnsupported
}
func (*darwinDriver) AttachWorkspace(context.Context, string, WorkspaceShare) error {
	return ErrUnsupported
}
func (*darwinDriver) DetachWorkspace(context.Context, string, WorkspaceShare) error {
	return ErrUnsupported
}
func (*darwinDriver) StartEgress(context.Context, string, *netjail.Allowlist) (io.Closer, error) {
	return nil, ErrUnsupported
}
