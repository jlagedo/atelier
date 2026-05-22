// Package vm orchestrates utility VMs above the raw HCS driver: it authors the
// compute-system doc from a high-level VMConfig, drives create/start/stop, and
// bridges each VM's serial console to the logs (design.md §7-§8). The broker
// owns a Manager and exposes it through gated RPC methods — the client never
// authors a compute-system doc itself (containment: §8).
package vm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jlagedo/atelier/services/internal/hcs"
	"github.com/jlagedo/atelier/services/internal/netjail"
)

// VMConfig is the host-facing description of a VM. KernelPath/RootFSPath are
// host file paths to a direct-boot kernel and our ext4 rootfs VHD. (For S1.2
// these are supplied by the dev CLI; once the bundle is pinned the broker will
// resolve them itself — S1.3/S6.)
type VMConfig struct {
	ID         string
	KernelPath string
	// InitrdPath is the host path to the matched boot initramfs (S1.3). Optional:
	// empty keeps the S1.2 built-in-driver boot (no initrd).
	InitrdPath string
	RootFSPath string
	MemoryMB   uint64
	CPUCount   int32
}

// ConsolePipeName is the named pipe a VM's serial console (COM1) is bridged to.
// Deterministic from the id so the doc and the listener always agree.
func ConsolePipeName(id string) string {
	return `\\.\pipe\atelier-con-` + id
}

// consoleStream is a per-VM serial-console bridge (platform-specific impl).
type consoleStream interface{ Close() error }

// Manager tracks the live VMs and their console bridges.
type Manager struct {
	drv hcs.Driver
	log *slog.Logger
	// egress is the Network-door policy (design.md §10 — S4.1) the per-VM
	// user-mode network enforces; the broker owns it and mutates it at runtime.
	egress *netjail.Allowlist

	mu  sync.Mutex
	vms map[string]*instance
}

type instance struct {
	cfg     VMConfig
	console consoleStream
	// egress is the per-VM host user-mode network (gvisor over hvsock), brought up
	// in Start and torn down in Stop. Nil until the VM is started (S4.1).
	egress *netjail.Network
	// runtimeID is the VM's hvsock partition GUID (the compute system's
	// RuntimeId), cached lazily on the first DialGuest. Empty until then.
	runtimeID string
	// shares tracks the VM's live 9p shares by tag → vsock port (S6.1: several
	// per-session shares can coexist in one VM). Guarded by Manager.mu.
	shares map[string]uint32
}

// NewManager returns a Manager backed by the platform HCS driver. egress is the
// Network-door allowlist the per-VM user-mode network consults (S4.1).
func NewManager(log *slog.Logger, egress *netjail.Allowlist) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{drv: hcs.New(), log: log, egress: egress, vms: make(map[string]*instance)}
}

// Create authors the compute-system doc for cfg and realizes the VM (not yet
// running). The console listener is brought up first so it is ready when the VM
// starts and HCS connects to it.
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

	pipe := ConsolePipeName(cfg.ID)
	doc, err := hcs.MakeLCOWDoc(hcs.DocConfig{
		Owner:          "atelier",
		KernelFilePath: cfg.KernelPath,
		InitrdPath:     cfg.InitrdPath,
		RootFSPath:     cfg.RootFSPath,
		MemoryMB:       cfg.MemoryMB,
		ProcessorCount: cfg.CPUCount,
		ConsolePipe:    pipe,
		// Root is immutable (CRIT-05): attach the rootfs read-only and boot `ro`. The
		// agent's writable surfaces are the 9p workspace/session shares and the boot-time
		// tmpfs mounts (image/guest/init.sh); the freshly-built ext4 is clean so a
		// read-only mount needs no journal recovery. A read-only image can also be shared
		// by several VMs. (Flip to false only for local rootfs debugging.)
		RootFSReadOnly: true,
	})
	if err != nil {
		return err
	}

	// The VM worker runs as a restricted virtual account; grant it access to the
	// files it must read (best-effort: log and continue if not required here).
	paths := []string{cfg.RootFSPath, cfg.KernelPath}
	if cfg.InitrdPath != "" {
		paths = append(paths, cfg.InitrdPath)
	}
	for _, p := range paths {
		if err := hcs.GrantVMAccess(cfg.ID, p); err != nil {
			m.log.Warn("grant vm access", "vm", cfg.ID, "path", p, "err", err)
		}
	}

	con, err := newConsoleStream(pipe, m.log.With("vm", cfg.ID))
	if err != nil {
		return fmt.Errorf("vm: console listen: %w", err)
	}

	if err := m.drv.Create(ctx, cfg.ID, doc); err != nil {
		_ = con.Close()
		return err
	}

	m.mu.Lock()
	m.vms[cfg.ID] = &instance{cfg: cfg, console: con}
	m.mu.Unlock()
	m.log.Info("vm created", "vm", cfg.ID, "console", pipe)
	return nil
}

// Start boots a previously-created VM and brings up its egress network (the
// Network door, design.md §10 — S4.1): the host user-mode network listener must
// be up before the guest's gvforwarder dials it. The guest side retries, so the
// ordering race is absorbed; if the listener can't start we log and continue —
// the guest then simply has no network (fail-closed, still safe).
func (m *Manager) Start(ctx context.Context, id string) error {
	inst, ok := m.get(id)
	if !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	if err := m.drv.Start(ctx, id); err != nil {
		return err
	}
	if eg, err := netjail.Start(m.log.With("vm", id), m.egress); err != nil {
		m.log.Warn("egress network start failed (guest will have no network)", "vm", id, "err", err)
	} else {
		m.mu.Lock()
		inst.egress = eg
		m.mu.Unlock()
	}
	m.log.Info("vm started", "vm", id)
	return nil
}

// Stop terminates a VM and tears down its console bridge.
func (m *Manager) Stop(ctx context.Context, id string) error {
	inst, ok := m.get(id)
	if !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	err := m.drv.Stop(ctx, id)
	if inst.console != nil {
		_ = inst.console.Close()
	}
	if inst.egress != nil {
		_ = inst.egress.Close()
	}
	m.mu.Lock()
	delete(m.vms, id)
	m.mu.Unlock()
	m.log.Info("vm stopped", "vm", id, "err", err)
	return err
}

// AttachWorkspace shares hostPath into the running VM as a 9p share named tag,
// served on vsock port (Files door, S3.1; concurrent per-session shares, S6.1):
// it grants the VM-worker account access to the folder, then adds the Plan9 share
// via ModifyComputeSystem. The guest still has to mount it (the broker drives
// guestd over Hop 3) — this is only the host half. Several shares (distinct
// tag/port) can coexist in one VM.
func (m *Manager) AttachWorkspace(ctx context.Context, id, hostPath string, readOnly bool, tag string, port uint32) error {
	inst, ok := m.get(id)
	if !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	if err := hcs.GrantVMAccess(id, hostPath); err != nil {
		m.log.Warn("grant vm access (workspace)", "vm", id, "path", hostPath, "err", err)
	}
	doc, err := hcs.MakePlan9AddRequest(hostPath, readOnly, tag, port)
	if err != nil {
		return err
	}
	if err := m.drv.Modify(ctx, id, doc); err != nil {
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

// DetachWorkspace removes the 9p share named tag (served on port) from the running
// VM (the host half of detach; the guest unmounts separately, driven by the
// broker).
func (m *Manager) DetachWorkspace(ctx context.Context, id, tag string, port uint32) error {
	inst, ok := m.get(id)
	if !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	doc, err := hcs.MakePlan9RemoveRequest(tag, port)
	if err != nil {
		return err
	}
	if err := m.drv.Modify(ctx, id, doc); err != nil {
		return err
	}
	m.mu.Lock()
	delete(inst.shares, tag)
	m.mu.Unlock()
	m.log.Info("workspace detached", "vm", id, "tag", tag)
	return nil
}

// Count is the number of tracked VMs (surfaced in getStatus.vmCount).
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
