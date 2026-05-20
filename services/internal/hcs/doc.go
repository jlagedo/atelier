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
)

// Default bind security descriptor for the VM's hvsocket: allow SYSTEM + the
// Builtin Administrators group full access. Same value hcsshim uses for LCOW.
const hvSocketBindSD = "D:P(A;;FA;;;SY)(A;;FA;;;BA)"

// DocConfig is the small, host-facing knob set we build a compute-system doc
// from. internal/vm fills it from a VMConfig.
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

	devices := &Devices{
		Scsi: map[string]Scsi{
			"0": {Attachments: map[string]Attachment{
				"0": {
					Type:     "VirtualDisk",
					Path:     c.RootFSPath,
					ReadOnly: c.RootFSReadOnly,
				},
			}},
		},
		HvSocket: &HvSocket2{
			HvSocketConfig: &HvSocketSystemConfig{
				DefaultBindSecurityDescriptor: hvSocketBindSD,
			},
		},
		// Plan9 (the /workspace 9p share) is intentionally absent until S3.1.
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
	fmt.Fprintf(&b, "root=/dev/sda %s init=/sbin/init", rw)
	return b.String()
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

// HvSocketSystemConfig sets default security descriptors for guest hvsockets.
type HvSocketSystemConfig struct {
	DefaultBindSecurityDescriptor    string `json:"DefaultBindSecurityDescriptor,omitempty"`
	DefaultConnectSecurityDescriptor string `json:"DefaultConnectSecurityDescriptor,omitempty"`
}

// Plan9 is the 9p file-share device (the /workspace share, wired in S3.1).
type Plan9 struct {
	Shares []Plan9Share `json:"Shares,omitempty"`
}

// Plan9Share is one host-folder→guest-mount 9p share.
type Plan9Share struct {
	Name         string   `json:"Name,omitempty"`
	AccessName   string   `json:"AccessName,omitempty"`
	Path         string   `json:"Path,omitempty"`
	Port         int32    `json:"Port,omitempty"`
	ReadOnly     bool     `json:"ReadOnly,omitempty"`
	AllowedFiles []string `json:"AllowedFiles,omitempty"`
}
