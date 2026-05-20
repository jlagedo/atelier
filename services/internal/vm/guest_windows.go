//go:build windows

package vm

import (
	"context"
	"fmt"
	"net"
	"time"

	winio "github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"

	"github.com/jlagedo/atelier/services/internal/vsock"
)

// DialGuest opens an AF_HYPERV (hvsock) connection to the guest daemon's vsock
// listener inside VM id (design.md §8 Hop 3, host side). The guest binds
// AF_VSOCK on vsock.GuestRPCPort; the host reaches it via the VsockServiceID
// template GUID, addressed at the VM's runtime partition GUID. The returned
// net.Conn plugs straight into rpc.NewClient.
func (m *Manager) DialGuest(ctx context.Context, id string) (net.Conn, error) {
	inst, ok := m.get(id)
	if !ok {
		return nil, fmt.Errorf("vm: %q not found", id)
	}

	m.mu.Lock()
	rid := inst.runtimeID
	m.mu.Unlock()
	if rid == "" {
		var err error
		rid, err = m.drv.RuntimeID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("vm: runtime id for %q: %w", id, err)
		}
		m.mu.Lock()
		inst.runtimeID = rid
		m.mu.Unlock()
	}

	vmID, err := guid.FromString(rid)
	if err != nil {
		return nil, fmt.Errorf("vm: bad runtime id %q for %q: %w", rid, id, err)
	}

	addr := &winio.HvsockAddr{
		VMID:      vmID,
		ServiceID: winio.VsockServiceID(vsock.GuestRPCPort),
	}
	// A few quick retries absorb the race between startVM returning and guestd
	// binding its vsock listener inside the booting guest.
	d := winio.HvsockDialer{Retries: 8, RetryWait: 250 * time.Millisecond}
	conn, err := d.Dial(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("vm: dial guest %q (vsock %d): %w", id, vsock.GuestRPCPort, err)
	}
	return conn, nil
}
