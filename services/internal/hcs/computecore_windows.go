//go:build windows

// Thin bindings over the documented HCS API in computecore.dll (the modern,
// operation-based surface). We deliberately roll our own (~8 calls) rather than
// import hcsshim's internal packages — see design.md §16 (own-bindings strategy).
//
// The async model is simple: every lifecycle call takes an HCS_OPERATION; it
// returns S_OK once the work is *queued*, and HcsWaitForOperationResult blocks
// until it actually finishes and hands back the result/error JSON. No callbacks,
// no polling.
package hcs

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// HCS handles are opaque pointers.
type (
	hcsSystem    uintptr
	hcsOperation uintptr
)

const (
	hcsTimeoutInfinite = 0xFFFFFFFF
	// requestedAccess for HcsOpenComputeSystem is reserved; docs require GENERIC_ALL.
	genericAll = 0x10000000
)

var (
	modcomputecore = windows.NewLazySystemDLL("computecore.dll")

	procHcsCreateOperation        = modcomputecore.NewProc("HcsCreateOperation")
	procHcsCloseOperation         = modcomputecore.NewProc("HcsCloseOperation")
	procHcsWaitForOperationResult = modcomputecore.NewProc("HcsWaitForOperationResult")
	procHcsCreateComputeSystem        = modcomputecore.NewProc("HcsCreateComputeSystem")
	procHcsOpenComputeSystem          = modcomputecore.NewProc("HcsOpenComputeSystem")
	procHcsStartComputeSystem         = modcomputecore.NewProc("HcsStartComputeSystem")
	procHcsModifyComputeSystem        = modcomputecore.NewProc("HcsModifyComputeSystem")
	procHcsTerminateComputeSystem     = modcomputecore.NewProc("HcsTerminateComputeSystem")
	procHcsCloseComputeSystem         = modcomputecore.NewProc("HcsCloseComputeSystem")
	procHcsGetComputeSystemProperties = modcomputecore.NewProc("HcsGetComputeSystemProperties")
	procHcsGrantVmAccess              = modcomputecore.NewProc("HcsGrantVmAccess")
)

// GrantVMAccess adds an access ACE so the VM worker process for vmID may read
// the file at filePath (kernel, rootfs VHD, …). HCS derives the VM's SID from
// vmID, so it must match the id passed to Create. Without this, the guest can't
// open its root disk and the boot fails.
func GrantVMAccess(vmID, filePath string) error {
	idp, err := windows.UTF16PtrFromString(vmID)
	if err != nil {
		return err
	}
	fpp, err := windows.UTF16PtrFromString(filePath)
	if err != nil {
		return err
	}
	if err := procHcsGrantVmAccess.Find(); err != nil {
		return err
	}
	r0, _, _ := syscall.SyscallN(
		procHcsGrantVmAccess.Addr(),
		uintptr(unsafe.Pointer(idp)),
		uintptr(unsafe.Pointer(fpp)),
	)
	return hresultError("HcsGrantVmAccess", r0, "")
}

// hresultError turns an HCS HRESULT (+ optional result document) into a Go
// error. Returns nil for any success HRESULT (high bit clear). HRESULTs in the
// FACILITY_WIN32 range are folded back to their plain Win32 code, matching how
// hcsshim surfaces them.
func hresultError(call string, r0 uintptr, doc string) error {
	if int32(r0) >= 0 {
		return nil
	}
	code := uint32(r0)
	win := code
	if win&0x1fff0000 == 0x00070000 {
		win &= 0xffff
	}
	msg := windows.Errno(win).Error()
	if doc != "" {
		return fmt.Errorf("%s: hresult=0x%08x (%s): %s", call, code, msg, doc)
	}
	return fmt.Errorf("%s: hresult=0x%08x (%s)", call, code, msg)
}

func createOperation() (hcsOperation, error) {
	if err := procHcsCreateOperation.Find(); err != nil {
		return 0, err
	}
	// context=NULL, callback=NULL → we wait synchronously via the operation.
	r0, _, _ := procHcsCreateOperation.Call(0, 0)
	if r0 == 0 {
		return 0, fmt.Errorf("HcsCreateOperation returned NULL")
	}
	return hcsOperation(r0), nil
}

func closeOperation(op hcsOperation) {
	if op == 0 {
		return
	}
	if err := procHcsCloseOperation.Find(); err == nil {
		_, _, _ = procHcsCloseOperation.Call(uintptr(op))
	}
}

// waitForOperationResultDoc blocks until the operation finishes and returns its
// result document (JSON) alongside the final status. The document (LocalAlloc'd
// by HCS) is read then freed. Calls that produce a payload (e.g. properties) use
// the document; lifecycle calls ignore it via waitForOperationResult.
func waitForOperationResultDoc(op hcsOperation, call string) (string, error) {
	if err := procHcsWaitForOperationResult.Find(); err != nil {
		return "", err
	}
	var result *uint16
	r0, _, _ := syscall.SyscallN(
		procHcsWaitForOperationResult.Addr(),
		uintptr(op),
		uintptr(uint32(hcsTimeoutInfinite)),
		uintptr(unsafe.Pointer(&result)),
	)
	doc := consumeResultDoc(result)
	return doc, hresultError(call, r0, doc)
}

// waitForOperationResult blocks until the operation finishes and returns its
// final status, discarding any result document.
func waitForOperationResult(op hcsOperation, call string) error {
	_, err := waitForOperationResultDoc(op, call)
	return err
}

// consumeResultDoc reads a PWSTR result document and frees it with LocalFree.
func consumeResultDoc(p *uint16) string {
	if p == nil {
		return ""
	}
	s := windows.UTF16PtrToString(p)
	_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(p)))
	return s
}

// hcsCreateComputeSystem creates the compute system described by config (our
// JSON doc) under the given id and blocks until creation completes. On success
// it returns a live system handle the caller must eventually close.
func hcsCreateComputeSystem(id, config string) (hcsSystem, error) {
	idp, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return 0, err
	}
	cfgp, err := windows.UTF16PtrFromString(config)
	if err != nil {
		return 0, err
	}
	op, err := createOperation()
	if err != nil {
		return 0, err
	}
	defer closeOperation(op)

	if err := procHcsCreateComputeSystem.Find(); err != nil {
		return 0, err
	}
	var system hcsSystem
	r0, _, _ := syscall.SyscallN(
		procHcsCreateComputeSystem.Addr(),
		uintptr(unsafe.Pointer(idp)),
		uintptr(unsafe.Pointer(cfgp)),
		uintptr(op),
		0, // securityDescriptor: reserved, must be NULL
		uintptr(unsafe.Pointer(&system)),
	)
	if int32(r0) < 0 {
		return 0, hresultError("HcsCreateComputeSystem", r0, "")
	}
	if err := waitForOperationResult(op, "HcsCreateComputeSystem"); err != nil {
		_ = hcsCloseComputeSystem(system)
		return 0, err
	}
	return system, nil
}

// hcsOpenComputeSystem opens a handle to an existing compute system by id.
func hcsOpenComputeSystem(id string) (hcsSystem, error) {
	idp, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return 0, err
	}
	if err := procHcsOpenComputeSystem.Find(); err != nil {
		return 0, err
	}
	var system hcsSystem
	r0, _, _ := syscall.SyscallN(
		procHcsOpenComputeSystem.Addr(),
		uintptr(unsafe.Pointer(idp)),
		uintptr(genericAll),
		uintptr(unsafe.Pointer(&system)),
	)
	if int32(r0) < 0 {
		return 0, hresultError("HcsOpenComputeSystem", r0, "")
	}
	return system, nil
}

// hcsGetComputeSystemProperties queries the system's properties and returns the
// result document (JSON). A NULL property query asks for the default property
// set, which carries the top-level "RuntimeId" used to address the guest over
// hvsock (Hop 3, S2.2).
func hcsGetComputeSystemProperties(system hcsSystem) (string, error) {
	op, err := createOperation()
	if err != nil {
		return "", err
	}
	defer closeOperation(op)

	if err := procHcsGetComputeSystemProperties.Find(); err != nil {
		return "", err
	}
	r0, _, _ := syscall.SyscallN(
		procHcsGetComputeSystemProperties.Addr(),
		uintptr(system),
		uintptr(op),
		0, // propertyQuery: NULL => default properties (includes RuntimeId)
	)
	if int32(r0) < 0 {
		return "", hresultError("HcsGetComputeSystemProperties", r0, "")
	}
	return waitForOperationResultDoc(op, "HcsGetComputeSystemProperties")
}

// hcsModifyComputeSystem applies a settings change (our ModifySettingRequest
// JSON) to a running system and blocks until it completes. Used to add/remove
// Plan9 /workspace shares at runtime (Files door, S3.1).
func hcsModifyComputeSystem(system hcsSystem, config string) error {
	cfgp, err := windows.UTF16PtrFromString(config)
	if err != nil {
		return err
	}
	op, err := createOperation()
	if err != nil {
		return err
	}
	defer closeOperation(op)

	if err := procHcsModifyComputeSystem.Find(); err != nil {
		return err
	}
	r0, _, _ := syscall.SyscallN(
		procHcsModifyComputeSystem.Addr(),
		uintptr(system),
		uintptr(op),
		uintptr(unsafe.Pointer(cfgp)),
		0, // identity: NULL
	)
	if int32(r0) < 0 {
		return hresultError("HcsModifyComputeSystem", r0, "")
	}
	return waitForOperationResult(op, "HcsModifyComputeSystem")
}

// hcsStartComputeSystem starts (boots) the system and blocks until start completes.
func hcsStartComputeSystem(system hcsSystem, options string) error {
	op, err := createOperation()
	if err != nil {
		return err
	}
	defer closeOperation(op)

	var optp *uint16
	if options != "" {
		if optp, err = windows.UTF16PtrFromString(options); err != nil {
			return err
		}
	}
	if err := procHcsStartComputeSystem.Find(); err != nil {
		return err
	}
	r0, _, _ := syscall.SyscallN(
		procHcsStartComputeSystem.Addr(),
		uintptr(system),
		uintptr(op),
		uintptr(unsafe.Pointer(optp)),
	)
	if int32(r0) < 0 {
		return hresultError("HcsStartComputeSystem", r0, "")
	}
	return waitForOperationResult(op, "HcsStartComputeSystem")
}

// hcsTerminateComputeSystem forcibly stops the system and blocks until done.
func hcsTerminateComputeSystem(system hcsSystem, options string) error {
	op, err := createOperation()
	if err != nil {
		return err
	}
	defer closeOperation(op)

	var optp *uint16
	if options != "" {
		if optp, err = windows.UTF16PtrFromString(options); err != nil {
			return err
		}
	}
	if err := procHcsTerminateComputeSystem.Find(); err != nil {
		return err
	}
	r0, _, _ := syscall.SyscallN(
		procHcsTerminateComputeSystem.Addr(),
		uintptr(system),
		uintptr(op),
		uintptr(unsafe.Pointer(optp)),
	)
	if int32(r0) < 0 {
		return hresultError("HcsTerminateComputeSystem", r0, "")
	}
	return waitForOperationResult(op, "HcsTerminateComputeSystem")
}

// hcsCloseComputeSystem releases a system handle (does not stop the VM).
//
// HcsCloseComputeSystem returns void (computecore API — "Return Values: None"), so
// there is no HRESULT to read. The syscall return register holds leftover garbage;
// feeding it to hresultError intermittently surfaced a high-bit-set value
// (e.g. 0x8f3a51f0 — a bogus HRESULT facility, not a real 0x8037xxxx HCS code) as a
// spurious "close failed", even though the handle was released fine. Make the call
// and return nil.
func hcsCloseComputeSystem(system hcsSystem) error {
	if system == 0 {
		return nil
	}
	if err := procHcsCloseComputeSystem.Find(); err != nil {
		return err
	}
	_, _, _ = syscall.SyscallN(procHcsCloseComputeSystem.Addr(), uintptr(system))
	return nil
}
