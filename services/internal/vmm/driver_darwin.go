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
	// DialGuest retry budget. Start only waits for the hypervisor "running" state,
	// not guest userspace, so the first dial after startVM can outrun guestd binding
	// its vsock listener. We retry on ECONNRESET ("guest not listening yet") across
	// ~10s — wider than the Windows hvsock dialer's 8×250ms because a darwin cold
	// boot (kernel + init + guestd) can take longer to reach a bound port.
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

// darwinDriver maps the platform-neutral VMM seam onto Apple's
// Virtualization.framework via the Code-Hex/vz cgo binding (Option A: the broker
// drives the VM in-process; no Swift helper). The binding owns one serial
// dispatch queue per VM and marshals every call onto it, satisfying the
// framework's threading rule without a hand-rolled queue here.
type darwinDriver struct {
	log *slog.Logger

	mu  sync.Mutex
	vms map[string]*darwinInstance
}

type darwinInstance struct {
	vm      *vz.VirtualMachine
	console *darwinConsole
	cfg     VMConfig
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
	bootOpts := []vz.LinuxBootLoaderOption{vz.WithCommandLine(darwinKernelCmdLine)}
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
	config.SetStorageDevicesVirtualMachineConfiguration([]vz.StorageDeviceConfiguration{blk})

	// Entropy: a virtio-rng source keeps Linux boot from stalling on early
	// getrandom() before the guest gathers its own entropy.
	entropy, err := vz.NewVirtioEntropyDeviceConfiguration()
	if err != nil {
		return fmt.Errorf("vm: entropy device: %w", err)
	}
	config.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{entropy})

	// Virtio-socket: the control-plane transport S5 dials guestd over (port 5000).
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

	// Serial console captured to broker logs (darwin analog of console_windows.go).
	console, consoleCfg, err := newDarwinConsole(d.log.With("vm", cfg.ID))
	if err != nil {
		return fmt.Errorf("vm: console: %w", err)
	}
	config.SetSerialPortsVirtualMachineConfiguration([]*vz.VirtioConsoleDeviceSerialPortConfiguration{consoleCfg})

	if ok, err := config.Validate(); err != nil {
		_ = console.Close()
		return fmt.Errorf("vm: validate config: %w", err)
	} else if !ok {
		_ = console.Close()
		return errors.New("vm: configuration is invalid")
	}

	vm, err := vz.NewVirtualMachine(config)
	if err != nil {
		_ = console.Close()
		return fmt.Errorf("vm: create: %w", err)
	}

	d.mu.Lock()
	d.vms[cfg.ID] = &darwinInstance{vm: vm, console: console, cfg: cfg}
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
	d.mu.Unlock()
	d.log.Info("vm running", "vm", id)
	return nil
}

// Stop asks the guest to stop, then forces a stop if it does not comply within
// the timeout (our minimal guest init has no ACPI shutdown handler), and tears
// down the console. Always drops the instance so the id can be recreated.
func (d *darwinDriver) Stop(ctx context.Context, id string) error {
	inst := d.instance(id)
	if inst == nil {
		return fmt.Errorf("vm: %q not found", id)
	}

	err := d.shutdown(ctx, inst.vm)
	if inst.console != nil {
		_ = inst.console.Close()
	}
	d.mu.Lock()
	delete(d.vms, id)
	d.mu.Unlock()
	d.log.Info("vm stopped", "vm", id, "err", err)
	return err
}

// shutdown drives a VM to the stopped state: a graceful RequestStop first, then a
// forceful Stop if it has not stopped in time.
func (d *darwinDriver) shutdown(ctx context.Context, vm *vz.VirtualMachine) error {
	if vm.State() == vz.VirtualMachineStateStopped {
		return nil
	}
	if vm.CanRequestStop() {
		if _, err := vm.RequestStop(); err == nil {
			if waitForState(ctx, vm, vz.VirtualMachineStateStopped, stopTimeout/2) == nil {
				return nil
			}
		}
	}
	if vm.CanStop() {
		if err := vm.Stop(); err != nil {
			return fmt.Errorf("vm: force stop: %w", err)
		}
		return waitForState(ctx, vm, vz.VirtualMachineStateStopped, stopTimeout)
	}
	return fmt.Errorf("vm: cannot stop (state %v)", vm.State())
}

// DialGuest opens a control-plane connection to guestd over the VM's virtio-socket
// device (validation #8, host CID 2 / guest CID 3). VZVirtioSocketConnection already
// satisfies net.Conn, so it is returned directly — no adapter. The vz binding marshals
// Connect onto the device's own dispatch queue, so no hand-rolled queue is needed here
// (validation #3). A bounded retry absorbs the race between Start returning (VM at the
// hypervisor "running" state) and guestd binding its vsock listener inside the still-
// booting guest: until guestd listens, Connect fails with ECONNRESET, which we retry.
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
		// ECONNRESET means guestd hasn't bound the port yet — retry. Any other
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
// Windows 9p path). The guest mounts the result with `mount -t virtiofs` (guestd).
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
