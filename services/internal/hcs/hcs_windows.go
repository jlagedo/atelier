//go:build windows

package hcs

import (
	"context"
	"fmt"
)

// TODO(M1): back this with github.com/Microsoft/hcsshim — author the
// compute-system JSON doc (kernel + ext4 rootfs VHD, KernelDirect, initrd),
// then HcsCreateComputeSystem + Start via hcsshim's uvm/hcs packages
// (design.md §7 VM image spec, §8 transport).
type winDriver struct{}

// New returns the Windows HCS driver.
func New() Driver { return winDriver{} }

func (winDriver) Create(_ context.Context, id string, _ []byte) error {
	return fmt.Errorf("hcs.Create(%s): not implemented yet (M1)", id)
}

func (winDriver) Start(_ context.Context, id string) error {
	return fmt.Errorf("hcs.Start(%s): not implemented yet (M1)", id)
}

func (winDriver) Stop(_ context.Context, id string) error {
	return fmt.Errorf("hcs.Stop(%s): not implemented yet (M1)", id)
}
