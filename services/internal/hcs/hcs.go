// Package hcs wraps the Windows Host Compute System: authoring the compute-system
// JSON doc and driving VM lifecycle (create/start/stop). The real implementation
// is Windows-only (hcsshim); see hcs_windows.go. (design.md §6-§8)
package hcs

import (
	"context"
	"errors"
)

// ErrUnsupported is returned by the non-Windows stub — HCS only exists on Windows.
var ErrUnsupported = errors.New("hcs: compute systems are only supported on windows")

// Driver drives a single utility VM through its lifecycle.
type Driver interface {
	Create(ctx context.Context, id string, doc []byte) error
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	// RuntimeID returns the VM's runtime GUID (the compute system's RuntimeId),
	// the partition identity the host uses to address the guest over hvsock
	// (Hop 3). It differs from the friendly id passed to Create.
	RuntimeID(ctx context.Context, id string) (string, error)
}
