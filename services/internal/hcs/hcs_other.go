//go:build !windows

package hcs

import "context"

// stubDriver lets the host build and run on non-Windows for development; any HCS
// call returns ErrUnsupported.
type stubDriver struct{}

// New returns the non-Windows stub driver.
func New() Driver { return stubDriver{} }

func (stubDriver) Create(context.Context, string, []byte) error      { return ErrUnsupported }
func (stubDriver) Start(context.Context, string) error               { return ErrUnsupported }
func (stubDriver) Stop(context.Context, string) error                { return ErrUnsupported }
func (stubDriver) RuntimeID(context.Context, string) (string, error) { return "", ErrUnsupported }
func (stubDriver) Modify(context.Context, string, []byte) error      { return ErrUnsupported }

// GrantVMAccess is a no-op off Windows (there is no HCS to grant access to).
func GrantVMAccess(_, _ string) error { return nil }
