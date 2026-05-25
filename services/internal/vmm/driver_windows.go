//go:build windows

package vmm

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	winio "github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"

	"github.com/jlagedo/atelier/services/internal/hcs"
	"github.com/jlagedo/atelier/services/internal/netjail"
	"github.com/jlagedo/atelier/services/internal/rpc"
	"github.com/jlagedo/atelier/services/internal/vsock"
)

// windowsDriver maps the platform-neutral VMM seam onto Windows HCS, hvsocket,
// Plan9, and gvisor-tap-vsock.
type windowsDriver struct {
	raw hcs.Driver
	log *slog.Logger

	mu  sync.Mutex
	vms map[string]*windowsInstance
}

type windowsInstance struct {
	console   consoleStream
	runtimeID string
}

// NewDriver returns the Windows HCS-backed VMM driver.
func NewDriver(log *slog.Logger) Driver {
	if log == nil {
		log = slog.Default()
	}
	return &windowsDriver{raw: hcs.New(), log: log, vms: make(map[string]*windowsInstance)}
}

// ConsolePipeName is the named pipe a VM's serial console (COM1) is bridged to.
// Deterministic from the id so the HCS doc and the listener always agree.
func ConsolePipeName(id string) string {
	return `\\.\pipe\atelier-con-` + id
}

func (d *windowsDriver) Create(ctx context.Context, cfg VMConfig) error {
	pipe := ConsolePipeName(cfg.ID)
	doc, err := hcs.MakeLCOWDoc(hcs.DocConfig{
		Owner:          "atelier",
		KernelFilePath: cfg.KernelPath,
		InitrdPath:     cfg.InitrdPath,
		RootFSPath:     cfg.RootFSPath,
		// runner ships as its own ro volume, SCSI-attached as a second disk (/dev/sdb)
		// and mounted by init.sh (LABEL=runner); it is not baked into the rootfs.
		RunnerImagePath: cfg.RunnerImagePath,
		MemoryMB:        cfg.MemoryMB,
		ProcessorCount:  cfg.CPUCount,
		ConsolePipe:     pipe,
		// Root is immutable (CRIT-05): attach the rootfs read-only and boot `ro`. The
		// agent's writable surfaces are the workspace/session shares and the boot-time
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
	if cfg.RunnerImagePath != "" {
		paths = append(paths, cfg.RunnerImagePath)
	}
	for _, p := range paths {
		if err := hcs.GrantVMAccess(cfg.ID, p); err != nil {
			d.log.Warn("grant vm access", "vm", cfg.ID, "path", p, "err", err)
		}
	}

	con, err := newConsoleStream(pipe, d.log.With("vm", cfg.ID))
	if err != nil {
		return fmt.Errorf("vm: console listen: %w", err)
	}

	if err := d.raw.Create(ctx, cfg.ID, doc); err != nil {
		_ = con.Close()
		return err
	}

	d.mu.Lock()
	d.vms[cfg.ID] = &windowsInstance{console: con}
	d.mu.Unlock()
	d.log.Info("vm console attached", "vm", cfg.ID, "console", pipe)
	return nil
}

func (d *windowsDriver) Start(ctx context.Context, id string) error {
	if err := d.raw.Start(ctx, id); err != nil {
		return err
	}
	return d.waitForRunner(ctx, id)
}

// waitForRunner polls the guest vsock until runner is accepting and processing RPCs.
// HCS Start() returns before the Linux guest boots; runner needs 30–90 s to come up.
// We verify readiness by sending setTime (seeds the clock as a side effect) with a
// per-attempt deadline so a queued-but-unprocessed connection does not block forever.
func (d *windowsDriver) waitForRunner(ctx context.Context, id string) error {
	const budget = 120 * time.Second
	const attemptDeadline = 6 * time.Second
	end := time.Now().Add(budget)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(end) {
			return fmt.Errorf("vm: runner in %q did not respond within %v", id, budget)
		}
		conn, err := d.DialGuest(ctx, id, vsock.GuestRPCPort)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(attemptDeadline))
		pingErr := rpc.NewClient(conn).Call(ctx, "setTime",
			map[string]any{"unixMs": time.Now().UnixMilli()}, nil)
		conn.Close()
		if pingErr == nil {
			d.log.Info("runner ready (clock seeded)", "vm", id)
			return nil
		}
		d.log.Debug("waitForRunner: not yet ready, retrying", "vm", id, "err", pingErr)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func (d *windowsDriver) Stop(ctx context.Context, id string) error {
	inst := d.instance(id)
	err := d.raw.Stop(ctx, id)
	if inst != nil && inst.console != nil {
		_ = inst.console.Close()
	}
	d.mu.Lock()
	delete(d.vms, id)
	d.mu.Unlock()
	return err
}

func (d *windowsDriver) DialGuest(ctx context.Context, id string, port uint32) (net.Conn, error) {
	inst := d.instance(id)
	if inst == nil {
		return nil, fmt.Errorf("vm: %q not found", id)
	}

	d.mu.Lock()
	rid := inst.runtimeID
	d.mu.Unlock()
	if rid == "" {
		var err error
		rid, err = d.raw.RuntimeID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("vm: runtime id for %q: %w", id, err)
		}
		d.mu.Lock()
		inst.runtimeID = rid
		d.mu.Unlock()
	}

	vmID, err := guid.FromString(rid)
	if err != nil {
		return nil, fmt.Errorf("vm: bad runtime id %q for %q: %w", rid, id, err)
	}

	addr := &winio.HvsockAddr{VMID: vmID, ServiceID: winio.VsockServiceID(port)}
	// A few quick retries absorb the race between startVM returning and runner
	// binding its vsock listener inside the booting guest.
	dialer := winio.HvsockDialer{Retries: 8, RetryWait: 250 * time.Millisecond}
	conn, err := dialer.Dial(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("vm: dial guest %q (vsock %d): %w", id, port, err)
	}
	return conn, nil
}

func (d *windowsDriver) AttachWorkspace(ctx context.Context, id string, share WorkspaceShare) error {
	if err := hcs.GrantVMAccess(id, share.HostPath); err != nil {
		d.log.Warn("grant vm access (workspace)", "vm", id, "path", share.HostPath, "err", err)
	}
	doc, err := hcs.MakePlan9AddRequest(share.HostPath, share.ReadOnly, share.Tag, share.Port)
	if err != nil {
		return err
	}
	return d.raw.Modify(ctx, id, doc)
}

func (d *windowsDriver) DetachWorkspace(ctx context.Context, id string, share WorkspaceShare) error {
	doc, err := hcs.MakePlan9RemoveRequest(share.Tag, share.Port)
	if err != nil {
		return err
	}
	return d.raw.Modify(ctx, id, doc)
}

func (d *windowsDriver) StartEgress(_ context.Context, id string, filter *netjail.Allowlist) (io.Closer, error) {
	ln, err := netjail.ListenHyperV()
	if err != nil {
		return nil, err
	}
	return netjail.Start(d.log.With("vm", id), filter, ln)
}

func (d *windowsDriver) instance(id string) *windowsInstance {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.vms[id]
}
