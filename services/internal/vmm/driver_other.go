//go:build !windows

package vmm

import (
	"context"
	"io"
	"log/slog"
	"net"

	"github.com/jlagedo/atelier/services/internal/netjail"
)

type unsupportedDriver struct{}

// NewDriver returns the non-Windows stub driver.
func NewDriver(*slog.Logger) Driver { return unsupportedDriver{} }

func (unsupportedDriver) Create(context.Context, VMConfig) error { return ErrUnsupported }
func (unsupportedDriver) Start(context.Context, string) error    { return ErrUnsupported }
func (unsupportedDriver) Stop(context.Context, string) error     { return ErrUnsupported }
func (unsupportedDriver) DialGuest(context.Context, string, uint32) (net.Conn, error) {
	return nil, ErrUnsupported
}
func (unsupportedDriver) AttachWorkspace(context.Context, string, WorkspaceShare) error {
	return ErrUnsupported
}
func (unsupportedDriver) DetachWorkspace(context.Context, string, WorkspaceShare) error {
	return ErrUnsupported
}
func (unsupportedDriver) StartEgress(context.Context, string, *netjail.Allowlist) (io.Closer, error) {
	return nil, ErrUnsupported
}
