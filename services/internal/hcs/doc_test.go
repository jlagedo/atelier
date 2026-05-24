package hcs

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMakeLCOWDocRequiredFields(t *testing.T) {
	if _, err := MakeLCOWDoc(DocConfig{RootFSPath: "r.vhd"}); err == nil {
		t.Fatal("expected error when KernelFilePath is empty")
	}
	if _, err := MakeLCOWDoc(DocConfig{KernelFilePath: "k"}); err == nil {
		t.Fatal("expected error when RootFSPath is empty")
	}
}

func TestMakeLCOWDocShape(t *testing.T) {
	raw, err := MakeLCOWDoc(DocConfig{
		Owner:          "atelier",
		KernelFilePath: `C:\boot\kernel`,
		RootFSPath:     `C:\boot\rootfs.vhd`,
		ConsolePipe:    `\\.\pipe\atelier-con`,
	})
	if err != nil {
		t.Fatalf("MakeLCOWDoc: %v", err)
	}

	// Round-trips into the typed model.
	var doc ComputeSystem
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if doc.SchemaVersion == nil || doc.SchemaVersion.Major != 2 || doc.SchemaVersion.Minor != 1 {
		t.Fatalf("want schema 2.1, got %+v", doc.SchemaVersion)
	}
	kd := doc.VirtualMachine.Chipset.LinuxKernelDirect
	if kd == nil || kd.KernelFilePath != `C:\boot\kernel` {
		t.Fatalf("kernel direct not set: %+v", kd)
	}
	if kd.InitRdPath != "" {
		t.Fatalf("S1.2 boots with no initrd, got InitRdPath=%q", kd.InitRdPath)
	}

	// Root disk is SCSI-attached as a VirtualDisk at controller0/lun0 (/dev/sda).
	att := doc.VirtualMachine.Devices.Scsi["0"].Attachments["0"]
	if att.Type != "VirtualDisk" || att.Path != `C:\boot\rootfs.vhd` {
		t.Fatalf("root attachment wrong: %+v", att)
	}
	// With no guestd volume configured, there is no second disk.
	if _, ok := doc.VirtualMachine.Devices.Scsi["0"].Attachments["1"]; ok {
		t.Fatalf("unexpected lun1 attachment without GuestdImagePath: %+v", doc.VirtualMachine.Devices.Scsi["0"].Attachments)
	}

	// Defaults applied.
	if got := doc.VirtualMachine.ComputeTopology.Memory.SizeInMB; got != 2048 {
		t.Fatalf("default memory want 2048, got %d", got)
	}
	if got := doc.VirtualMachine.ComputeTopology.Processor.Count; got != 2 {
		t.Fatalf("default cpu want 2, got %d", got)
	}

	// Serial console wired to the named pipe + matching cmdline.
	if cp := doc.VirtualMachine.Devices.ComPorts[0]; cp.NamedPipe != `\\.\pipe\atelier-con` {
		t.Fatalf("com port not wired: %+v", doc.VirtualMachine.Devices.ComPorts)
	}
	if !strings.Contains(kd.KernelCmdLine, "console=ttyS0") ||
		!strings.Contains(kd.KernelCmdLine, "root=/dev/sda") ||
		!strings.Contains(kd.KernelCmdLine, "init=/sbin/init") {
		t.Fatalf("cmdline missing expected tokens: %q", kd.KernelCmdLine)
	}

	// The whole point of own-bindings: NO Microsoft GCS in the cmdline.
	if strings.Contains(kd.KernelCmdLine, "gcs") || strings.Contains(kd.KernelCmdLine, "vsockexec") {
		t.Fatalf("cmdline must not reference gcs/vsockexec: %q", kd.KernelCmdLine)
	}
}

func TestMakeLCOWDocGuestdVolume(t *testing.T) {
	raw, err := MakeLCOWDoc(DocConfig{
		KernelFilePath:  "k",
		RootFSPath:      `C:\boot\rootfs.vhd`,
		GuestdImagePath: `C:\boot\guestd.vhd`,
	})
	if err != nil {
		t.Fatalf("MakeLCOWDoc: %v", err)
	}
	var doc ComputeSystem
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// guestd volume rides controller0/lun1 (/dev/sdb), always read-only; init.sh
	// mounts it by LABEL=guestd and execs guestd from it (it's not baked into the rootfs).
	att := doc.VirtualMachine.Devices.Scsi["0"].Attachments["1"]
	if att.Type != "VirtualDisk" || att.Path != `C:\boot\guestd.vhd` {
		t.Fatalf("guestd attachment wrong: %+v", att)
	}
	if !att.ReadOnly {
		t.Fatalf("guestd volume must be read-only, got %+v", att)
	}
}

func TestMakeLCOWDocCmdLineOverride(t *testing.T) {
	raw, err := MakeLCOWDoc(DocConfig{
		KernelFilePath: "k",
		RootFSPath:     "r.vhd",
		KernelCmdLine:  "custom root=/dev/sdb",
	})
	if err != nil {
		t.Fatalf("MakeLCOWDoc: %v", err)
	}
	var doc ComputeSystem
	_ = json.Unmarshal(raw, &doc)
	if got := doc.VirtualMachine.Chipset.LinuxKernelDirect.KernelCmdLine; got != "custom root=/dev/sdb" {
		t.Fatalf("override ignored: %q", got)
	}
}
