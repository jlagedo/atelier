// This file authors the HCS *compute-system document* — the JSON blob handed to
// HcsCreateComputeSystem that fully describes our utility VM. It is the model
// captured from hcsshim's makeLCOWDoc (schema 2.1), deliberately re-implemented
// here (own-bindings strategy, design.md §16) and stripped of Microsoft's GCS:
// no /bin/vsockexec, no /bin/gcs cmdline tail, init points at OUR /sbin/init.
//
// Pure data + encoding/json, so it builds and tests on any OS; only the syscall
// layer (vmcompute_windows.go) is Windows-only.
package hcs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jlagedo/atelier/services/internal/vsock"
)

// Default security descriptor for the VM's hvsocket (used for both bind and
// connect): allow SYSTEM + the Builtin Administrators group full access. Same
// value hcsshim uses for LCOW. The connect SD is what lets the host process
// dial the guest's vsock listener (Hop 3, S2.2) — without it the host's
// AF_HYPERV connect is refused.
const hvSocketSD = "D:P(A;;FA;;;SY)(A;;FA;;;BA)"

// Plan9 share flags (private in the HCS schema; values mirror hcsshim). The
// LinuxMetadata flag makes the 9p server carry Unix uid/gid/mode so the guest
// sees correct ownership/permissions on the /workspace files (S3.1).
const (
	plan9FlagReadOnly      int32 = 0x00000001
	plan9FlagLinuxMetadata int32 = 0x00000004
)

// DocConfig is the small, host-facing knob set we build a compute-system doc
// from. The Windows vmm driver fills it from a VMConfig.
type DocConfig struct {
	// Owner is a free-form tag HCS records against the system (e.g. "atelier").
	Owner string
	// KernelFilePath is the host path to a direct-boot kernel (vmlinux/vmlinuz).
	// For S1.2 this is a built-in-driver kernel (LCOW/WSL2) so no initrd is
	// needed; S1.3 swaps in the matched generic-Ubuntu kernel + initrd.
	KernelFilePath string
	// InitrdPath is optional; leave empty for the S1.2 built-in-driver boot.
	InitrdPath string
	// RootFSPath is the host path to our ext4 rootfs VHD (SCSI-attached as the
	// root disk, surfaced in-guest as /dev/sda).
	RootFSPath string
	// RootFSReadOnly attaches the root disk read-only (default false: rw).
	RootFSReadOnly bool

	// RunnerImagePath, when set, attaches the runner volume (its own ro ext4-in-VHD)
	// as a second SCSI disk (surfaced in-guest as /dev/sdb). init.sh mounts it by
	// LABEL=runner and execs runner from it — runner is not baked into the rootfs.
	RunnerImagePath string

	// MemoryMB / ProcessorCount default to 2048 / 2 when zero.
	MemoryMB       uint64
	ProcessorCount int32

	// ConsolePipe, when set, wires COM1 (ttyS0) to this named pipe so we can
	// capture the guest's serial boot log. ConsolePort selects the ComPorts map
	// index (COM1 == 0).
	ConsolePipe string
	ConsolePort uint32

	// KernelCmdLine overrides the default cmdline. Empty = built from the
	// fields above: "console=ttyS0,115200 root=/dev/sda rw init=/sbin/init".
	KernelCmdLine string
}

// MakeLCOWDoc builds and marshals the compute-system document for a Linux
// utility VM that direct-boots KernelFilePath and mounts RootFSPath as root.
func MakeLCOWDoc(c DocConfig) ([]byte, error) {
	if c.KernelFilePath == "" {
		return nil, fmt.Errorf("hcs: DocConfig.KernelFilePath is required")
	}
	if c.RootFSPath == "" {
		return nil, fmt.Errorf("hcs: DocConfig.RootFSPath is required")
	}

	mem := c.MemoryMB
	if mem == 0 {
		mem = 2048
	}
	cpu := c.ProcessorCount
	if cpu == 0 {
		cpu = 2
	}

	// SCSI controller 0: the rootfs at LUN 0 (/dev/sda) and, when shipped, the runner
	// volume at LUN 1 (/dev/sdb, always read-only). init.sh mounts the latter by label.
	scsiAttachments := map[string]Attachment{
		"0": {
			Type:     "VirtualDisk",
			Path:     c.RootFSPath,
			ReadOnly: c.RootFSReadOnly,
		},
	}
	if c.RunnerImagePath != "" {
		scsiAttachments["1"] = Attachment{
			Type:     "VirtualDisk",
			Path:     c.RunnerImagePath,
			ReadOnly: true,
		}
	}

	devices := &Devices{
		Scsi: map[string]Scsi{
			"0": {Attachments: scsiAttachments},
		},
		HvSocket: &HvSocket2{
			HvSocketConfig: &HvSocketSystemConfig{
				DefaultBindSecurityDescriptor:    hvSocketSD,
				DefaultConnectSecurityDescriptor: hvSocketSD,
				// Authorize the egress link service (Network door, S4.1) per-VM so
				// the guest's gvforwarder may connect to our host-side gvisor network
				// on it. HCS does this implicitly for its own services (e.g. the 9p
				// share); ours is a plain host listener, so we list it here — the
				// per-VM analogue of a GuestCommunicationServices registry entry, and
				// the reason guest->host on this port routes without a host reboot.
				ServiceTable: map[string]HvSocketServiceConfig{
					egressServiceGUID(): {
						BindSecurityDescriptor:    hvSocketSD,
						ConnectSecurityDescriptor: hvSocketSD,
						AllowWildcardBinds:        true,
					},
				},
			},
		},
		// An empty Plan9 controller so /workspace shares can be added/removed on
		// the *running* VM via ModifyComputeSystem (the Files door, design.md §10
		// — S3.1). Workspaces attach/detach at runtime (one long-lived VM, no
		// reboot to swap), so no share is baked into the boot doc.
		Plan9: &Plan9{},
	}
	if c.ConsolePipe != "" {
		devices.ComPorts = map[uint32]ComPort{
			c.ConsolePort: {NamedPipe: c.ConsolePipe},
		}
	}

	doc := ComputeSystem{
		Owner:                             c.Owner,
		SchemaVersion:                     &Version{Major: 2, Minor: 1},
		ShouldTerminateOnLastHandleClosed: true,
		VirtualMachine: &VirtualMachine{
			StopOnReset: true,
			Chipset: &Chipset{
				LinuxKernelDirect: &LinuxKernelDirect{
					KernelFilePath: c.KernelFilePath,
					InitRdPath:     c.InitrdPath,
					KernelCmdLine:  defaultCmdLine(c),
				},
			},
			ComputeTopology: &Topology{
				Memory:    &Memory2{SizeInMB: mem, AllowOvercommit: true},
				Processor: &Processor2{Count: cpu},
			},
			Devices: devices,
		},
	}

	return json.Marshal(doc)
}

// defaultCmdLine returns the explicit override if set, else a minimal Linux
// direct-boot cmdline: serial console on ttyS0, root on the SCSI VHD, our init.
// Crucially this carries NO gcs/vsockexec tail (that is what locks hcsshim's
// LCOW path to Microsoft's guest agent — see S0a).
func defaultCmdLine(c DocConfig) string {
	if c.KernelCmdLine != "" {
		return c.KernelCmdLine
	}
	rw := "rw"
	if c.RootFSReadOnly {
		rw = "ro"
	}
	var b strings.Builder
	if c.ConsolePipe != "" {
		b.WriteString("console=ttyS0,115200 ")
	}
	// noresume: a utility VM never hibernates, so skip the Ubuntu initramfs
	// resume (hibernate) premount probe, which otherwise stalls boot ~30s waiting
	// for a swap/resume device that doesn't exist (observed S1.3).
	fmt.Fprintf(&b, "root=/dev/sda %s noresume init=/sbin/init", rw)
	return b.String()
}

// plan9ShareResourcePath is the HCS resource path for the Plan9 share list,
// addressed by ModifyComputeSystem to add/remove shares on a running VM.
const plan9ShareResourcePath = "VirtualMachine/Devices/Plan9/Shares"

// MakePlan9AddRequest builds the ModifyComputeSystem document that adds a 9p
// share (host folder hostPath, share name/AccessName tag, served on vsock port)
// to a running VM (Files door, S3.1; concurrent per-session shares, S6.1). It
// carries only the host-side Settings — no GuestRequest — because we run no GCS:
// the guest (runner) mounts the share itself over our control plane. tag/port
// must be unique among a VM's live shares so several can coexist.
func MakePlan9AddRequest(hostPath string, readOnly bool, tag string, port uint32) ([]byte, error) {
	flags := plan9FlagLinuxMetadata
	if readOnly {
		flags |= plan9FlagReadOnly
	}
	return json.Marshal(modifySettingRequest{
		ResourcePath: plan9ShareResourcePath,
		RequestType:  "Add",
		Settings: Plan9Share{
			Name:       tag,
			AccessName: tag,
			Path:       hostPath,
			Port:       int32(port),
			Flags:      flags,
			ReadOnly:   readOnly,
		},
	})
}

// MakePlan9RemoveRequest builds the ModifyComputeSystem document that removes the
// 9p share identified by tag/port from a running VM (the host side of detach).
func MakePlan9RemoveRequest(tag string, port uint32) ([]byte, error) {
	return json.Marshal(modifySettingRequest{
		ResourcePath: plan9ShareResourcePath,
		RequestType:  "Remove",
		Settings: Plan9Share{
			Name:       tag,
			AccessName: tag,
			Port:       int32(port),
		},
	})
}

// modifySettingRequest is the document passed to HcsModifyComputeSystem. We omit
// the GuestRequest field (no GCS); the guest is driven over our own RPC instead.
type modifySettingRequest struct {
	ResourcePath string `json:"ResourcePath,omitempty"`
	RequestType  string `json:"RequestType,omitempty"`
	Settings     any    `json:"Settings,omitempty"`
}

// --- Schema 2.1 compute-system document (subset we author) ------------------
// Field names and JSON tags mirror Microsoft/hcsshim's schema2 package so the
// document HCS receives is byte-compatible with what the real lib would send.

// ComputeSystem is the root document passed to HcsCreateComputeSystem.
type ComputeSystem struct {
	Owner                             string          `json:"Owner,omitempty"`
	SchemaVersion                     *Version        `json:"SchemaVersion,omitempty"`
	VirtualMachine                    *VirtualMachine `json:"VirtualMachine,omitempty"`
	ShouldTerminateOnLastHandleClosed bool            `json:"ShouldTerminateOnLastHandleClosed,omitempty"`
}

// Version is the HCS schema version (we target 2.1).
type Version struct {
	Major int32 `json:"Major"`
	Minor int32 `json:"Minor"`
}

// VirtualMachine describes the VM itself.
type VirtualMachine struct {
	StopOnReset     bool      `json:"StopOnReset,omitempty"`
	Chipset         *Chipset  `json:"Chipset,omitempty"`
	ComputeTopology *Topology `json:"ComputeTopology,omitempty"`
	Devices         *Devices  `json:"Devices,omitempty"`
}

// Chipset carries the direct-boot configuration (no UEFI/BIOS for LCOW).
type Chipset struct {
	LinuxKernelDirect *LinuxKernelDirect `json:"LinuxKernelDirect,omitempty"`
}

// LinuxKernelDirect is the KernelDirect boot: a kernel file, optional initrd,
// and the kernel command line.
type LinuxKernelDirect struct {
	KernelFilePath string `json:"KernelFilePath,omitempty"`
	InitRdPath     string `json:"InitRdPath,omitempty"`
	KernelCmdLine  string `json:"KernelCmdLine,omitempty"`
}

// Topology is the VM's memory + processor allocation.
type Topology struct {
	Memory    *Memory2    `json:"Memory,omitempty"`
	Processor *Processor2 `json:"Processor,omitempty"`
}

// Memory2 is the memory block of the topology.
type Memory2 struct {
	SizeInMB        uint64 `json:"SizeInMB"`
	AllowOvercommit bool   `json:"AllowOvercommit,omitempty"`
}

// Processor2 is the processor block of the topology.
type Processor2 struct {
	Count int32 `json:"Count"`
}

// Devices is the set of virtual devices attached to the VM.
type Devices struct {
	ComPorts map[uint32]ComPort `json:"ComPorts,omitempty"`
	Scsi     map[string]Scsi    `json:"Scsi,omitempty"`
	HvSocket *HvSocket2         `json:"HvSocket,omitempty"`
	Plan9    *Plan9             `json:"Plan9,omitempty"`
}

// ComPort bridges a guest serial port to a host named pipe (our boot console).
type ComPort struct {
	NamedPipe           string `json:"NamedPipe,omitempty"`
	OptimizeForDebugger bool   `json:"OptimizeForDebugger,omitempty"`
}

// Scsi is one SCSI controller with its disk attachments (keyed "0".."N").
type Scsi struct {
	Attachments map[string]Attachment `json:"Attachments,omitempty"`
}

// Attachment is a single disk on a SCSI controller. Type is "VirtualDisk" for
// a VHD/VHDX (our ext4 rootfs).
type Attachment struct {
	Type     string `json:"Type,omitempty"`
	Path     string `json:"Path,omitempty"`
	ReadOnly bool   `json:"ReadOnly,omitempty"`
}

// HvSocket2 holds the VM's hvsocket configuration (Hop 3 transport, S2.x).
type HvSocket2 struct {
	HvSocketConfig *HvSocketSystemConfig `json:"HvSocketConfig,omitempty"`
}

// HvSocketSystemConfig sets default security descriptors for guest hvsockets,
// plus a per-service-GUID ServiceTable for services that need explicit
// authorization (e.g. the egress link the guest connects to — S4.1).
type HvSocketSystemConfig struct {
	DefaultBindSecurityDescriptor    string                           `json:"DefaultBindSecurityDescriptor,omitempty"`
	DefaultConnectSecurityDescriptor string                           `json:"DefaultConnectSecurityDescriptor,omitempty"`
	ServiceTable                     map[string]HvSocketServiceConfig `json:"ServiceTable,omitempty"`
}

// HvSocketServiceConfig is the per-service hvsocket authorization (field names
// mirror hcsshim's schema2 so HCS accepts the doc).
type HvSocketServiceConfig struct {
	BindSecurityDescriptor    string `json:"BindSecurityDescriptor,omitempty"`
	ConnectSecurityDescriptor string `json:"ConnectSecurityDescriptor,omitempty"`
	AllowWildcardBinds        bool   `json:"AllowWildcardBinds,omitempty"`
	Disabled                  bool   `json:"Disabled,omitempty"`
}

// egressServiceGUID is the Hyper-V vsock service GUID for the egress link port
// (template "<port-8hex>-facb-11e6-bd58-64006a7986d3"); the guest connects to the
// host on it (Network door, S4.1).
func egressServiceGUID() string {
	return fmt.Sprintf("%08x-facb-11e6-bd58-64006a7986d3", vsock.EgressLinkPort)
}

// Plan9 is the 9p file-share device (the /workspace share, wired in S3.1).
type Plan9 struct {
	Shares []Plan9Share `json:"Shares,omitempty"`
}

// Plan9Share is one host-folder→guest-mount 9p share.
type Plan9Share struct {
	Name       string `json:"Name,omitempty"`
	AccessName string `json:"AccessName,omitempty"`
	Path       string `json:"Path,omitempty"`
	Port       int32  `json:"Port,omitempty"`
	// Flags carries the share's behavior bits (LinuxMetadata, ReadOnly, …);
	// private in the HCS schema, mirrored from hcsshim. See plan9Flag* consts.
	Flags        int32    `json:"Flags,omitempty"`
	ReadOnly     bool     `json:"ReadOnly,omitempty"`
	AllowedFiles []string `json:"AllowedFiles,omitempty"`
}
