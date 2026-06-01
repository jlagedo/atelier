//go:build darwin

package vmm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"

	vz "github.com/Code-Hex/vz/v3"

	"github.com/jlagedo/atelier/services/internal/netjail"
	"github.com/jlagedo/atelier/services/internal/vsock"
)

// VM resource defaults and lifecycle timeouts. The memory/CPU defaults mirror
// the Windows HCS path (internal/hcs/doc.go) so a zero VMConfig boots the same
// shape on either platform; VZ rejects a zero count/size outright.
const (
	defaultMemoryMB uint64 = 2048
	defaultCPUCount uint   = 2
	startTimeout           = 30 * time.Second
	stopTimeout            = 15 * time.Second
	// watchInterval is how often the per-VM crash watcher polls vm.State() for an
	// unexpected exit. We poll State() rather than reading StateChangedNotify()
	// because the binding's notify channel is single-consumer and waitForState
	// already drains it during Start/Stop — a second reader would steal transitions.
	watchInterval = 2 * time.Second
	// DialGuest retry budget. Start only waits for the hypervisor "running" state,
	// not guest userspace, so the first dial after startVM can outrun runner binding
	// its vsock listener. We retry on ECONNRESET ("guest not listening yet") across
	// ~10s — wider than the Windows hvsock dialer's 8×250ms because a darwin cold
	// boot (kernel + init + runner) can take longer to reach a bound port.
	dialGuestRetries   = 40
	dialGuestRetryWait = 250 * time.Millisecond
	// darwinKernelCmdLine boots our bundle under Virtualization.framework. It
	// differs from the Windows cmdline (internal/hcs/doc.go) in two ways that are
	// hard boot blockers if wrong: the rootfs is virtio-blk (/dev/vda, not the
	// SCSI /dev/sda HCS exposes) and the console is a virtio console (hvc0, not
	// ttyS0). `ro` keeps the read-only-root invariant (CRIT-05); `noresume` skips
	// the Ubuntu initramfs hibernate-resume probe that otherwise stalls boot.
	darwinKernelCmdLine = "console=hvc0 root=/dev/vda ro noresume init=/sbin/init"
)

// The debug console (hvc1 + a root shell outside the cage) is gated by the `debugconsole`
// build tag, NOT a runtime flag: debugConsoleToken() and newDebugConsolePort() are the seam,
// implemented live in debugconsole_on_darwin.go (debug builds) and inert in
// debugconsole_off_darwin.go (release). Release binaries contain none of that code.

// darwinDriver maps the platform-neutral VMM seam onto Apple's
// Virtualization.framework via the Code-Hex/vz cgo binding (Option A: the broker
// drives the VM in-process; no Swift helper). The binding owns one serial
// dispatch queue per VM and marshals every call onto it, satisfying the
// framework's threading rule without a hand-rolled queue here.
type darwinDriver struct {
	log *slog.Logger

	mu  sync.Mutex
	vms map[string]*darwinInstance
	// onUnexpectedExit, if set (via SetOnUnexpectedExit), is invoked by the per-VM
	// watcher after it reaps a VM that reached a terminal state on its own (crash,
	// guest OOM, framework error). The Manager registers it to tear down its own
	// per-VM resources (egress, time-sync). Nil = no host-side hook (e.g. tests).
	onUnexpectedExit func(id string)
}

type darwinInstance struct {
	vm      *vz.VirtualMachine
	console *darwinConsole
	// debugConsole is the optional interactive root shell over a second virtio console
	// (hvc1). Non-nil only in debug builds (the `debugconsole` tag); nil in release.
	debugConsole io.Closer
	cfg          VMConfig
	// socket is the runtime virtio-socket device, cached on Start for S5's
	// DialGuest (VZVirtioSocketDevice.connect(toPort:)). Nil until started.
	socket *vz.VirtioSocketDevice
	// fsdev is the runtime virtio-fs device, cached on Start for S6's AttachWorkspace
	// (VZVirtioFileSystemDevice.share swap on the live device). Nil until started.
	fsdev *vz.VirtioFileSystemDevice
	// shares is the authoritative tag->dir set the host has applied to fsdev. The device
	// exposes no readable getter, so the driver tracks the set here and rebuilds the whole
	// share on every attach/detach. Guarded by darwinDriver.mu.
	shares map[string]*vz.SharedDirectory
	// intentional marks that Stop() is driving this VM to a terminal state, so the
	// watcher does not mislabel an operator-initiated stop as a crash. Guarded by
	// darwinDriver.mu.
	intentional bool
	// onExit is the host-side crash hook stamped from darwinDriver.onUnexpectedExit at
	// Start; the watcher calls it after reaping a VM that died on its own. Nil = no hook.
	onExit func(id string)
}

// SetOnUnexpectedExit registers a hook invoked when the per-VM watcher reaps a VM that
// reached a terminal state without an operator Stop(). It is an optional capability the
// Manager discovers by type assertion, so the cross-platform Driver interface stays
// unchanged (the Windows/stub drivers simply do not implement it). Call before Start.
func (d *darwinDriver) SetOnUnexpectedExit(fn func(id string)) {
	d.mu.Lock()
	d.onUnexpectedExit = fn
	d.mu.Unlock()
}

// NewDriver returns the macOS Virtualization.framework VMM driver.
func NewDriver(log *slog.Logger) Driver {
	if log == nil {
		log = slog.Default()
	}
	return &darwinDriver{log: log, vms: make(map[string]*darwinInstance)}
}

// Create builds the VZ configuration and instantiates the VM, but does not start
// it (matching the windows driver's create/start split). The device set is the
// boot loader, the read-only rootfs, a virtio-socket device (S5), an empty
// virtio-fs device (S6), the NAT crutch (see below), and the serial console.
func (d *darwinDriver) Create(_ context.Context, cfg VMConfig) error {
	if cfg.ID == "" {
		return errors.New("vm: config has empty ID")
	}
	d.mu.Lock()
	_, exists := d.vms[cfg.ID]
	d.mu.Unlock()
	if exists {
		return fmt.Errorf("vm: %q already exists", cfg.ID)
	}

	cpu := defaultCPUCount
	if cfg.CPUCount > 0 {
		cpu = uint(cfg.CPUCount)
	}
	memMB := cfg.MemoryMB
	if memMB == 0 {
		memMB = defaultMemoryMB
	}

	// The bundle ships a decompressed arm64 Image for the VZ target (image/build.sh
	// gunzips the kernel for darwin); VZLinuxBootLoader cannot boot a gzip vmlinuz.
	// debugConsoleToken() is empty in release builds (the seam is inert), so the cmdline
	// is unchanged there; debug builds append the hvc1 handshake token.
	cmdLine := darwinKernelCmdLine
	if t := debugConsoleToken(); t != "" {
		cmdLine += " " + t
	}
	bootOpts := []vz.LinuxBootLoaderOption{vz.WithCommandLine(cmdLine)}
	if cfg.InitrdPath != "" {
		bootOpts = append(bootOpts, vz.WithInitrd(cfg.InitrdPath))
	}
	bootLoader, err := vz.NewLinuxBootLoader(cfg.KernelPath, bootOpts...)
	if err != nil {
		return fmt.Errorf("vm: boot loader: %w", err)
	}

	config, err := vz.NewVirtualMachineConfiguration(bootLoader, cpu, memMB*1024*1024)
	if err != nil {
		return fmt.Errorf("vm: configuration: %w", err)
	}

	// Root disk: raw ext4 attached read-only (validation #6 / CRIT-05). VZ reads
	// the raw image directly — no VHD wrapper like the Windows path.
	disk, err := vz.NewDiskImageStorageDeviceAttachment(cfg.RootFSPath, true)
	if err != nil {
		return fmt.Errorf("vm: rootfs attachment: %w", err)
	}
	blk, err := vz.NewVirtioBlockDeviceConfiguration(disk)
	if err != nil {
		return fmt.Errorf("vm: rootfs block device: %w", err)
	}
	storage := []vz.StorageDeviceConfiguration{blk}

	// runner volume: its own ro ext4 image attached as a second disk (-> /dev/vdb).
	// init.sh mounts it by label (LABEL=runner) and execs runner from it, so runner
	// iterates without rebuilding the rootfs. Same trust model as the rootfs above
	// (ro, opaque image; no host-fs mapping). Empty path = baked runner (no second disk).
	if cfg.RunnerImagePath != "" {
		runnerDisk, err := vz.NewDiskImageStorageDeviceAttachment(cfg.RunnerImagePath, true)
		if err != nil {
			return fmt.Errorf("vm: runner volume attachment: %w", err)
		}
		runnerBlk, err := vz.NewVirtioBlockDeviceConfiguration(runnerDisk)
		if err != nil {
			return fmt.Errorf("vm: runner volume block device: %w", err)
		}
		storage = append(storage, runnerBlk)
		d.log.Info("attaching runner volume", "vm", cfg.ID, "path", cfg.RunnerImagePath)
	}
	config.SetStorageDevicesVirtualMachineConfiguration(storage)

	// Entropy: a virtio-rng source keeps Linux boot from stalling on early
	// getrandom() before the guest gathers its own entropy.
	entropy, err := vz.NewVirtioEntropyDeviceConfiguration()
	if err != nil {
		return fmt.Errorf("vm: entropy device: %w", err)
	}
	config.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{entropy})

	// Virtio-socket: the control-plane transport S5 dials runner over (port 5000).
	sock, err := vz.NewVirtioSocketDeviceConfiguration()
	if err != nil {
		return fmt.Errorf("vm: socket device: %w", err)
	}
	config.SetSocketDevicesVirtualMachineConfiguration([]vz.SocketDeviceConfiguration{sock})

	// Virtio-fs: one device, tagged for the default workspace, with an empty share.
	// S6's AttachWorkspace swaps this device's share at runtime (the forked vz binding
	// exposes VZVirtioFileSystemDevice.share get/set); it's created here so the device
	// exists from boot for the guest to mount.
	fs, err := vz.NewVirtioFileSystemDeviceConfiguration(vsock.WorkspaceShareTag)
	if err != nil {
		return fmt.Errorf("vm: filesystem device: %w", err)
	}
	emptyShare, err := vz.NewMultipleDirectoryShare(map[string]*vz.SharedDirectory{})
	if err != nil {
		return fmt.Errorf("vm: empty share: %w", err)
	}
	fs.SetDirectoryShare(emptyShare)
	config.SetDirectorySharingDevicesVirtualMachineConfiguration([]vz.DirectorySharingDeviceConfiguration{fs})

	// No network device: the guest has no real NIC. All egress flows through the
	// gvisor-tap-vsock jail re-hosted over the VZ vsock listener (StartEgress), so
	// containment is the vsock jail alone (S9 dropped the S4 NAT crutch).

	// Serial console captured to broker logs (darwin analog of console_windows.go). It
	// stays FIRST so the kernel's console=hvc0 boot log lands here untouched.
	console, consoleCfg, err := newDarwinConsole(d.log.With("vm", cfg.ID))
	if err != nil {
		return fmt.Errorf("vm: console: %w", err)
	}
	ports := []*vz.VirtioConsoleDeviceSerialPortConfiguration{consoleCfg}

	// Optional dev-only debug console as a SECOND serial port -> /dev/hvc1 in the guest,
	// bridged to a unix socket an interactive client attaches to. The seam is live only in
	// debug builds (`debugconsole` tag); in release newDebugConsolePort returns nil and no
	// second port is added.
	debugConsole, dbgPort, derr := newDebugConsolePort(cfg.ID, d.log.With("vm", cfg.ID))
	if derr != nil {
		_ = console.Close()
		return fmt.Errorf("vm: debug console: %w", derr)
	}
	if dbgPort != nil {
		ports = append(ports, dbgPort)
	}
	config.SetSerialPortsVirtualMachineConfiguration(ports)

	closeConsoles := func() {
		_ = console.Close()
		if debugConsole != nil {
			_ = debugConsole.Close()
		}
	}
	if ok, err := config.Validate(); err != nil {
		closeConsoles()
		return fmt.Errorf("vm: validate config: %w", err)
	} else if !ok {
		closeConsoles()
		return errors.New("vm: configuration is invalid")
	}

	vm, err := vz.NewVirtualMachine(config)
	if err != nil {
		closeConsoles()
		return fmt.Errorf("vm: create: %w", err)
	}

	d.mu.Lock()
	d.vms[cfg.ID] = &darwinInstance{vm: vm, console: console, debugConsole: debugConsole, cfg: cfg}
	d.mu.Unlock()
	d.log.Info("vm created", "vm", cfg.ID, "cpu", cpu, "memMB", memMB)
	return nil
}

// Start boots the VM and waits for it to reach the running state so a failed boot
// surfaces synchronously, then caches the runtime socket device for S5.
func (d *darwinDriver) Start(ctx context.Context, id string) error {
	inst := d.instance(id)
	if inst == nil {
		return fmt.Errorf("vm: %q not found", id)
	}
	if !inst.vm.CanStart() {
		return fmt.Errorf("vm: %q cannot start (state %v)", id, inst.vm.State())
	}
	if err := inst.vm.Start(); err != nil {
		return fmt.Errorf("vm: start %q: %w", id, err)
	}
	if err := waitForState(ctx, inst.vm, vz.VirtualMachineStateRunning, startTimeout); err != nil {
		return fmt.Errorf("vm: %q did not reach running: %w", id, err)
	}
	// Cache the runtime devices the control plane and files door dial into. They only
	// exist after the VM reaches running; we configured exactly one of each in Create.
	d.mu.Lock()
	if devs := inst.vm.SocketDevices(); len(devs) > 0 {
		inst.socket = devs[0]
	}
	if devs := inst.vm.DirectorySharingDevices(); len(devs) > 0 {
		inst.fsdev = devs[0]
	}
	inst.onExit = d.onUnexpectedExit
	d.mu.Unlock()
	d.log.Info("vm running", "vm", id)
	// Watch for an unexpected guest exit so a crashed cage is reaped, not left
	// phantom-alive in the maps. Detached: it outlives Start and runs until the VM
	// is reaped (here or by Stop).
	go d.watch(id, inst)
	return nil
}

// watch polls the VM's state and reaps it if it reaches a terminal state without an
// operator Stop() (kernel panic, guest OOM, framework error). Polling vm.State() avoids
// stealing transitions from waitForState, which is the sole consumer of the binding's
// single-consumer StateChangedNotify channel.
func (d *darwinDriver) watch(id string, inst *darwinInstance) {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for range ticker.C {
		// Already reaped (by Stop or a prior tick) — nothing left to watch.
		if d.instance(id) == nil {
			return
		}
		s := inst.vm.State()
		if s != vz.VirtualMachineStateStopped && s != vz.VirtualMachineStateError {
			continue
		}
		d.mu.Lock()
		intentional := inst.intentional
		onExit := inst.onExit
		d.mu.Unlock()
		if intentional {
			return // our own Stop() is driving this teardown; not a crash
		}
		d.log.Error("cage exited unexpectedly — reaping", "vm", id, "state", s)
		d.reap(id)
		if onExit != nil {
			onExit(id) // host-side teardown (egress, time-sync), outside d.mu
		}
		return
	}
}

// Stop hard-stops the VM (our minimal guest init installs no ACPI handler, so a
// graceful ACPI RequestStop is dead weight) and, once it confirms a terminal state,
// reaps it — drops the instance and tears down the consoles — so the id can be
// recreated. A force-stop that leaves the VM non-terminal retains the handle and
// returns the error rather than orphaning a still-live VM.
func (d *darwinDriver) Stop(ctx context.Context, id string) error {
	inst := d.instance(id)
	if inst == nil {
		return fmt.Errorf("vm: %q not found", id)
	}
	// Mark intentional so the watcher does not race in and log this teardown as a crash.
	d.mu.Lock()
	inst.intentional = true
	d.mu.Unlock()

	err := d.shutdown(ctx, inst.vm)
	if s := inst.vm.State(); s == vz.VirtualMachineStateStopped || s == vz.VirtualMachineStateError {
		d.reap(id)
		d.log.Info("vm stopped", "vm", id, "err", err)
	} else {
		// Force-stop failed and the VM is still Running/Stopping — keep the only
		// handle to it so it can be retried/inspected (and the watcher can still
		// reap it if it later dies) instead of leaking a VM a same-id createVM
		// would collide with.
		d.log.Warn("vm did not stop; retaining handle", "vm", id, "state", s, "err", err)
	}
	return err
}

// reap removes the instance from the driver map and tears down its consoles exactly
// once. Map presence under d.mu is the single-winner guard, so Stop and the watcher
// can both call reap and only the first one does the teardown.
func (d *darwinDriver) reap(id string) {
	d.mu.Lock()
	inst := d.vms[id]
	if inst == nil {
		d.mu.Unlock()
		return
	}
	delete(d.vms, id)
	d.mu.Unlock()
	if inst.console != nil {
		_ = inst.console.Close()
	}
	if inst.debugConsole != nil {
		_ = inst.debugConsole.Close()
	}
}

// shutdown drives a VM to the stopped state. Terminal states (Stopped/Error) are a
// no-op. Otherwise it hard-stops: the guest init installs no ACPI/poweroff handler,
// so the old graceful RequestStop only ever timed out — we skip straight to Stop().
func (d *darwinDriver) shutdown(ctx context.Context, vm *vz.VirtualMachine) error {
	switch vm.State() {
	case vz.VirtualMachineStateStopped, vz.VirtualMachineStateError:
		return nil
	}
	if vm.CanStop() {
		if err := vm.Stop(); err != nil {
			return fmt.Errorf("vm: force stop: %w", err)
		}
		return waitForState(ctx, vm, vz.VirtualMachineStateStopped, stopTimeout)
	}
	return fmt.Errorf("vm: cannot stop (state %v)", vm.State())
}

// DialGuest opens a control-plane connection to runner over the VM's virtio-socket
// device (validation #8, host CID 2 / guest CID 3). VZVirtioSocketConnection already
// satisfies net.Conn, so it is returned directly — no adapter. The vz binding marshals
// Connect onto the device's own dispatch queue, so no hand-rolled queue is needed here
// (validation #3). A bounded retry absorbs the race between Start returning (VM at the
// hypervisor "running" state) and runner binding its vsock listener inside the still-
// booting guest: until runner listens, Connect fails with ECONNRESET, which we retry.
func (d *darwinDriver) DialGuest(ctx context.Context, id string, port uint32) (net.Conn, error) {
	inst := d.instance(id)
	if inst == nil {
		return nil, fmt.Errorf("vm: %q not found", id)
	}
	d.mu.Lock()
	sock := inst.socket
	d.mu.Unlock()
	if sock == nil {
		return nil, fmt.Errorf("vm: %q has no socket device (not started?)", id)
	}

	var lastErr error
	for attempt := 0; attempt < dialGuestRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := sock.Connect(port)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		// ECONNRESET means runner hasn't bound the port yet — retry. Any other
		// error is terminal (no device, framework failure, etc.).
		var nserr *vz.NSError
		if !errors.As(err, &nserr) || nserr.Code != int(syscall.ECONNRESET) {
			return nil, fmt.Errorf("vm: dial guest %q (vsock %d): %w", id, port, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(dialGuestRetryWait):
		}
	}
	return nil, fmt.Errorf("vm: dial guest %q (vsock %d): %w", id, port, lastErr)
}

// AttachWorkspace shares a host folder into the running guest over virtio-fs (S6).
// VZ has no incremental "add directory" call: the share is swapped wholesale, so the
// driver keeps the authoritative tag->dir set in inst.shares, folds in the new entry,
// rebuilds the VZDirectoryShare, and SetShares it on the live device. share.Port is
// ignored — virtio-fs is tag-addressed, not vsock-port-addressed (that field is the
// Windows 9p path). The guest mounts the result with `mount -t virtiofs` (runner).
func (d *darwinDriver) AttachWorkspace(_ context.Context, id string, share WorkspaceShare) error {
	if err := validateShareTag(share.Tag); err != nil {
		return err
	}
	inst := d.instance(id)
	if inst == nil {
		return fmt.Errorf("vm: %q not found", id)
	}
	// NewSharedDirectory os.Stat()s the path; the broker already canonicalized it.
	sd, err := vz.NewSharedDirectory(share.HostPath, share.ReadOnly)
	if err != nil {
		return fmt.Errorf("vm: shared directory %q: %w", share.HostPath, err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if inst.fsdev == nil {
		return fmt.Errorf("vm: %q has no filesystem device (not started?)", id)
	}
	next := cloneShares(inst.shares)
	next[share.Tag] = sd
	dshare, err := buildShare(next)
	if err != nil {
		return fmt.Errorf("vm: build share: %w", err)
	}
	inst.fsdev.SetShare(dshare)
	inst.shares = next
	return nil
}

// DetachWorkspace drops a tag from the live virtio-fs share (S6). Idempotent: removing
// an absent tag is a no-op. Like AttachWorkspace, it rebuilds and swaps the whole share.
func (d *darwinDriver) DetachWorkspace(_ context.Context, id string, share WorkspaceShare) error {
	inst := d.instance(id)
	if inst == nil {
		return fmt.Errorf("vm: %q not found", id)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if inst.fsdev == nil {
		return fmt.Errorf("vm: %q has no filesystem device (not started?)", id)
	}
	if _, ok := inst.shares[share.Tag]; !ok {
		return nil
	}
	next := cloneShares(inst.shares)
	delete(next, share.Tag)
	dshare, err := buildShare(next)
	if err != nil {
		return fmt.Errorf("vm: build share: %w", err)
	}
	inst.fsdev.SetShare(dshare)
	inst.shares = next
	return nil
}

// cloneShares copies the tag->dir set so a failed rebuild never leaves the tracked state
// half-mutated (SetShare is applied, then the new set is committed).
func cloneShares(in map[string]*vz.SharedDirectory) map[string]*vz.SharedDirectory {
	out := make(map[string]*vz.SharedDirectory, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// buildShare maps the tracked set onto a VZDirectoryShare. The lone legacy
// "workspace" tag uses a SingleDirectoryShare so the guest's `mount -t virtiofs workspace
// /workspace` lands the directory directly at the device root (the S6 single-workspace
// shape). Every other set — a single per-session tag, or two or more of anything — uses a
// MultipleDirectoryShare, which exposes each entry as a named subdirectory under the device
// root. Pinning sessions to MultipleDirectoryShare keeps the layout stable at
// <base>/<tag> for ANY session count (S7): a lone session no longer collapses to the root,
// so adding a second session never flips an existing one's path. Zero entries clears the
// device with an empty MultipleDirectoryShare.
func buildShare(shares map[string]*vz.SharedDirectory) (vz.DirectoryShare, error) {
	if len(shares) == 1 {
		if sd, ok := shares[vsock.WorkspaceShareTag]; ok {
			return vz.NewSingleDirectoryShare(sd)
		}
	}
	return vz.NewMultipleDirectoryShare(shares)
}

// validateShareTag bounds the share tag before it reaches the framework / guest mount.
// Apple's VZVirtioFileSystemDeviceConfiguration rejects tags of 36+ bytes; the same bound
// is applied here, with a conservative charset that is also a safe virtio-fs directory name.
func validateShareTag(tag string) error {
	if tag == "" {
		return errors.New("vm: empty share tag")
	}
	if len(tag) >= 36 {
		return fmt.Errorf("vm: share tag %q too long (max 35 bytes)", tag)
	}
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		ok := c == '-' || c == '_' || c == '.' ||
			(c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !ok {
			return fmt.Errorf("vm: invalid share tag %q (allowed: A-Z a-z 0-9 . _ -)", tag)
		}
	}
	return nil
}

// StartEgress re-hosts the gvisor-tap-vsock jail over a VZ vsock listener. The
// guest has no real NIC (S9 dropped the NAT crutch); its gvforwarder dials the
// host on vsock.EgressLinkPort, which we accept via the cached socket device (the
// inbound counterpart to DialGuest's Connect) and hand to the shared jail. The
// returned *netjail.Network is the io.Closer the Manager closes on Stop.
func (d *darwinDriver) StartEgress(_ context.Context, id string, filter *netjail.Allowlist) (io.Closer, error) {
	inst := d.instance(id)
	if inst == nil {
		return nil, fmt.Errorf("vm: %q not found", id)
	}
	d.mu.Lock()
	sock := inst.socket
	d.mu.Unlock()
	if sock == nil {
		return nil, fmt.Errorf("vm: %q has no socket device (not started?)", id)
	}
	ln, err := sock.Listen(vsock.EgressLinkPort)
	if err != nil {
		return nil, fmt.Errorf("vm: egress listen: %w", err)
	}
	return netjail.Start(d.log.With("vm", id), filter, ln)
}

func (d *darwinDriver) instance(id string) *darwinInstance {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.vms[id]
}

// waitForState blocks until vm reaches want, the VM enters the error state, the
// context is cancelled, or the timeout elapses. The notify channel is fetched
// before re-checking State so a transition can't slip through the gap.
func waitForState(ctx context.Context, vm *vz.VirtualMachine, want vz.VirtualMachineState, timeout time.Duration) error {
	ch := vm.StateChangedNotify()
	if vm.State() == want {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case s := <-ch:
			if s == want {
				return nil
			}
			if s == vz.VirtualMachineStateError {
				return errors.New("vm entered error state")
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("timeout after %s (state %v)", timeout, vm.State())
		}
	}
}
