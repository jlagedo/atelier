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

	// Virtio-fs: one device, tagged for the default workspace, with an empty
	// share for now. S6 mutates its share to mount workspaces; created here so the
	// device exists at boot.
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

	// TEMPORARY (validation #4): NAT needs no entitlement and gives the guest real
	// egress, which is enough to confirm the VM is alive in this boot spike. S9
	// removes this and routes egress through the gvisor-tap-vsock jail instead.
	nat, err := vz.NewNATNetworkDeviceAttachment()
	if err != nil {
		return fmt.Errorf("vm: nat attachment: %w", err)
	}
	netCfg, err := vz.NewVirtioNetworkDeviceConfiguration(nat)
	if err != nil {
		return fmt.Errorf("vm: network device: %w", err)
	}
	config.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{netCfg})

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
	if devs := inst.vm.SocketDevices(); len(devs) > 0 {
		d.mu.Lock()
		inst.socket = devs[0]
		d.mu.Unlock()
	}
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

// DialGuest reaches guestd over the virtio-socket device. Implemented in S5.
func (*darwinDriver) DialGuest(context.Context, string, uint32) (net.Conn, error) {
	return nil, ErrUnsupported
}

// AttachWorkspace mounts a host folder over virtio-fs. Implemented in S6.
func (*darwinDriver) AttachWorkspace(context.Context, string, WorkspaceShare) error {
	return ErrUnsupported
}

// DetachWorkspace removes a virtio-fs share. Implemented in S6.
func (*darwinDriver) DetachWorkspace(context.Context, string, WorkspaceShare) error {
	return ErrUnsupported
}

// StartEgress re-hosts the gvisor-tap-vsock jail over a VZ vsock listener.
// Implemented in S9 (the NAT attachment above is the interim egress path).
func (*darwinDriver) StartEgress(context.Context, string, *netjail.Allowlist) (io.Closer, error) {
	return nil, ErrUnsupported
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
