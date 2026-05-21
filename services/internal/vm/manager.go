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

	mu  sync.Mutex
	vms map[string]*instance
}

type instance struct {
	cfg     VMConfig
	console consoleStream
	// runtimeID is the VM's hvsock partition GUID (the compute system's
	// RuntimeId), cached lazily on the first DialGuest. Empty until then.
	runtimeID string
}

// NewManager returns a Manager backed by the platform HCS driver.
func NewManager(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{drv: hcs.New(), log: log, vms: make(map[string]*instance)}
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

// Start boots a previously-created VM.
func (m *Manager) Start(ctx context.Context, id string) error {
	if _, ok := m.get(id); !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	if err := m.drv.Start(ctx, id); err != nil {
		return err
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
	m.mu.Lock()
	delete(m.vms, id)
	m.mu.Unlock()
	m.log.Info("vm stopped", "vm", id, "err", err)
	return err
}

// AttachWorkspace shares hostPath into the running VM as the /workspace 9p share
// (Files door, S3.1): it grants the VM-worker account access to the folder, then
// adds the Plan9 share via ModifyComputeSystem. The guest still has to mount it
// (the broker drives guestd over Hop 3) — this is only the host half.
func (m *Manager) AttachWorkspace(ctx context.Context, id, hostPath string, readOnly bool) error {
	if _, ok := m.get(id); !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	if err := hcs.GrantVMAccess(id, hostPath); err != nil {
		m.log.Warn("grant vm access (workspace)", "vm", id, "path", hostPath, "err", err)
	}
	doc, err := hcs.MakePlan9AddRequest(hostPath, readOnly)
	if err != nil {
		return err
	}
	if err := m.drv.Modify(ctx, id, doc); err != nil {
		return err
	}
	m.log.Info("workspace attached", "vm", id, "path", hostPath, "readOnly", readOnly)
	return nil
}

// DetachWorkspace removes the /workspace 9p share from the running VM (the host
// half of detach; the guest unmounts separately, driven by the broker).
func (m *Manager) DetachWorkspace(ctx context.Context, id string) error {
	if _, ok := m.get(id); !ok {
		return fmt.Errorf("vm: %q not found", id)
	}
	doc, err := hcs.MakePlan9RemoveRequest()
	if err != nil {
		return err
	}
	if err := m.drv.Modify(ctx, id, doc); err != nil {
		return err
	}
	m.log.Info("workspace detached", "vm", id)
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
