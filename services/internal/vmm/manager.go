package vmm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/jlagedo/atelier/services/internal/netjail"
	"github.com/jlagedo/atelier/services/internal/rpc"
	"github.com/jlagedo/atelier/services/internal/vsock"
)

// Manager tracks live VMs and delegates platform-specific work to a Driver.
type Manager struct {
	drv Driver
	log *slog.Logger
	// egress is the Network-door policy the per-VM user-mode network enforces;
	// the broker owns it and mutates it at runtime.
	egress *netjail.Allowlist

	mu  sync.Mutex
	vms map[string]*instance
}

type instance struct {
	cfg VMConfig
	// egress is the per-VM host user-mode network, brought up in Start and torn
	// down in Stop. Nil until the VM is started or when the platform fails closed.
	egress io.Closer
	// shares tracks the VM's live workspace shares by tag -> port. Guarded by
	// Manager.mu.
	shares map[string]uint32
	// stopSync cancels the per-VM time-resync goroutine (Start → Stop). Nil until
	// the VM is started. Guarded by Manager.mu.
	stopSync context.CancelFunc
}

// NewManager returns a Manager backed by the platform's default driver.
func NewManager(log *slog.Logger, egress *netjail.Allowlist) *Manager {
	return NewManagerWithDriver(log, egress, NewDriver(log))
}

// NewManagerWithDriver returns a Manager with an explicit driver, primarily for
// tests and future platform wiring.
func NewManagerWithDriver(log *slog.Logger, egress *netjail.Allowlist, drv Driver) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if drv == nil {
		drv = NewDriver(log)
	}
	return &Manager{drv: drv, log: log, egress: egress, vms: make(map[string]*instance)}
}

// Create realizes the VM through the platform driver, then records it as live.
func (m *Manager) Create(ctx context.Context, cfg VMConfig) error {
	if cfg.ID == "" {
		return errors.New("vm: config has empty ID")
	}
	m.mu.Lock()
	_, exists := m.vms[cfg.ID]
	m.mu.Unlock()
	if exists {
		return fmt.Errorf("vm: %q already exists", cfg.ID)
	}

	if err := m.drv.Create(ctx, cfg); err != nil {
		return err
	}

	m.mu.Lock()
	m.vms[cfg.ID] = &instance{cfg: cfg}
	m.mu.Unlock()
	m.log.Info("vm created", "vm", cfg.ID)
	return nil
}

// Start boots a previously-created VM and asks the driver to bring up the
// Network door. If egress cannot start, the VM still runs with no network
// (fail-closed, matching the previous Windows behavior).
func (m *Manager) Start(ctx context.Context, id string) error {
	inst, ok := m.get(id)
	if !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	if err := m.drv.Start(ctx, id); err != nil {
		return err
	}
	if eg, err := m.drv.StartEgress(ctx, id, m.egress); err != nil {
		m.log.Warn("egress network start failed (guest will have no network)", "vm", id, "err", err)
	} else if eg != nil {
		m.mu.Lock()
		inst.egress = eg
		m.mu.Unlock()
	}
	// Push the host wall clock into the guest: once now (boot seed) and every 30s
	// after (self-heals after host sleep). Detached context — it must outlive Start
	// and live until Stop cancels it.
	syncCtx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	inst.stopSync = cancel
	m.mu.Unlock()
	go m.syncTimeLoop(syncCtx, id)
	m.log.Info("vm started", "vm", id)
	return nil
}

// Stop terminates a VM and tears down tracked resources.
func (m *Manager) Stop(ctx context.Context, id string) error {
	inst, ok := m.get(id)
	if !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	err := m.drv.Stop(ctx, id)
	if inst.egress != nil {
		_ = inst.egress.Close()
	}
	m.mu.Lock()
	if inst.stopSync != nil {
		inst.stopSync()
		inst.stopSync = nil
	}
	delete(m.vms, id)
	m.mu.Unlock()
	m.log.Info("vm stopped", "vm", id, "err", err)
	return err
}

// DialGuest opens a connection to runner's control-plane RPC port.
func (m *Manager) DialGuest(ctx context.Context, id string) (net.Conn, error) {
	if _, ok := m.get(id); !ok {
		return nil, fmt.Errorf("vm: %q not found", id)
	}
	conn, err := m.drv.DialGuest(ctx, id, vsock.GuestRPCPort)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// SeedTime pushes the host's current wall-clock time to the guest's CLOCK_REALTIME
// over the control plane (Hop 3). The host is the time source — the broker stamps
// time.Now() so no timestamp ever rides a JS wire. Used by both the setTime door
// and the periodic resync loop. The guest has no other clock source, so this
// always steps (no drift threshold); stepping CLOCK_REALTIME leaves CLOCK_MONOTONIC
// untouched.
func (m *Manager) SeedTime(ctx context.Context, id string) error {
	if _, ok := m.get(id); !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	conn, err := m.DialGuest(ctx, id)
	if err != nil {
		return err
	}
	defer conn.Close()
	return rpc.NewClient(conn).Call(ctx, "setTime",
		map[string]any{"id": id, "unixMs": time.Now().UnixMilli()}, nil)
}

// syncTimeLoop seeds the guest clock immediately (boot seed) then every 30s
// (self-heals after host sleep/resume within one tick). Errors are logged and
// retried — a failed sync must never crash the goroutine or the broker. The boot
// seed leans on DialGuest's retry to absorb runner not yet listening.
func (m *Manager) syncTimeLoop(ctx context.Context, id string) {
	if err := m.SeedTime(ctx, id); err != nil {
		m.log.Warn("boot time seed failed (will retry next tick)", "vm", id, "err", err)
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.SeedTime(ctx, id); err != nil {
				m.log.Warn("periodic time seed failed (will retry next tick)", "vm", id, "err", err)
			}
		}
	}
}

// AttachWorkspace shares hostPath into the running VM as a tagged workspace
// share. Several shares can coexist in one VM when their tags/ports differ.
func (m *Manager) AttachWorkspace(ctx context.Context, id, hostPath string, readOnly bool, tag string, port uint32) error {
	inst, ok := m.get(id)
	if !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	share := WorkspaceShare{HostPath: hostPath, ReadOnly: readOnly, Tag: tag, Port: port}
	if err := m.drv.AttachWorkspace(ctx, id, share); err != nil {
		return err
	}
	m.mu.Lock()
	if inst.shares == nil {
		inst.shares = make(map[string]uint32)
	}
	inst.shares[tag] = port
	m.mu.Unlock()
	m.log.Info("workspace attached", "vm", id, "path", hostPath, "tag", tag, "port", port, "readOnly", readOnly)
	return nil
}

// DetachWorkspace removes the tagged workspace share from the running VM.
func (m *Manager) DetachWorkspace(ctx context.Context, id, tag string, port uint32) error {
	inst, ok := m.get(id)
	if !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	share := WorkspaceShare{Tag: tag, Port: port}
	if err := m.drv.DetachWorkspace(ctx, id, share); err != nil {
		return err
	}
	m.mu.Lock()
	delete(inst.shares, tag)
	m.mu.Unlock()
	m.log.Info("workspace detached", "vm", id, "tag", tag)
	return nil
}

// Count is the number of tracked VMs surfaced in getStatus.vmCount.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.vms)
}

func (m *Manager) get(id string) (*instance, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.vms[id]
	return inst, ok
}
