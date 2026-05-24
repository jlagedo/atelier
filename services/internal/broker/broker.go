// Package broker is the containment chokepoint between the unprivileged app and
// the privileged host work: it registers the RPC method surface and runs every
// capability use through the policy gate + audit log (design.md §8, §10).
package broker

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/jlagedo/atelier/services/internal/netjail"
	"github.com/jlagedo/atelier/services/internal/rpc"
	"github.com/jlagedo/atelier/services/internal/vmm"
	"github.com/jlagedo/atelier/services/internal/vsock"
)

// Version is the host service version.
const Version = "0.0.0"

// Broker holds the policy gate, audit logger, VM manager, and process start time.
type Broker struct {
	start time.Time
	log   *slog.Logger
	gate  Gate
	vms   *vmm.Manager

	// egress is the Network-door policy (design.md §10 — S4.1): a runtime-settable,
	// default-deny allowlist the guest's user-mode network (gvisor) consults. The
	// same pointer is handed to the VM manager, so setEgressPolicy mutates the live
	// object the network enforces — no reboot to change policy.
	egress *netjail.Allowlist

	// mu guards workspace + mounts. workspace is the canonicalized host folder of
	// the default (legacy single) share — the Files-door jail root readFile/
	// writeFile resolve against (design.md §10 — S3.1); empty = door closed.
	// mounts is the S6.1 generalization: every live 9p share keyed by its tag, so
	// many per-session folders can be attached to the one shared VM at once
	// (the default share, if any, is also tracked here under WorkspaceShareTag).
	mu        sync.Mutex
	workspace string
	mounts    map[string]mountInfo

	// opLocks serializes the multi-step attach/detach sequence per VM (files.go):
	// each call drives HCS (async ModifyComputeSystem, which rejects duplicate
	// Plan9 share names) + guestd + the mounts map across several awaits, so two
	// concurrent calls for the same VM must not interleave — otherwise a failing
	// add rolls back and orphans a share, or a swap unmounts one mid-use. This is
	// separate from mu (which only guards the mounts map for the fast readFile/
	// writeFile path). Lock order is always opLock → mu, never the reverse.
	opmu    sync.Mutex
	opLocks map[string]*sync.Mutex
}

// mountInfo records one live 9p share so detach can remove the exact host-side
// Plan9 share (tag/port) and unmount the right guest path (S6.1).
type mountInfo struct {
	hostPath  string
	guestPath string
	tag       string
	port      uint32
	readOnly  bool
}

// New returns a Broker. A nil logger uses slog.Default(); a nil gate uses the
// dev-time AllowAll gate. The Files door starts closed; a workspace is attached
// at runtime via attachWorkspace (no reboot to swap).
func New(log *slog.Logger, gate Gate) *Broker {
	if log == nil {
		log = slog.Default()
	}
	if gate == nil {
		gate = AllowAll{log: log}
	}
	egress := netjail.NewAllowlist(log)
	return &Broker{
		start:   time.Now(),
		log:     log,
		gate:    gate,
		vms:     vmm.NewManager(log, egress),
		egress:  egress,
		mounts:  make(map[string]mountInfo),
		opLocks: make(map[string]*sync.Mutex),
	}
}

// currentWorkspace returns the attached Files-door root, or "" if none.
func (b *Broker) currentWorkspace() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.workspace
}

// setWorkspace records (or clears, with "") the attached Files-door root.
func (b *Broker) setWorkspace(root string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.workspace = root
}

// addMount registers a live share (S6.1) and allocates a free vsock port when
// m.port is 0. Returns the stored mountInfo (with the resolved port).
func (b *Broker) addMount(m mountInfo) mountInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	if m.port == 0 {
		m.port = b.allocPortLocked()
	}
	b.mounts[m.tag] = m
	return m
}

// removeMount drops a share by tag, returning it (and whether it existed).
func (b *Broker) removeMount(tag string) (mountInfo, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m, ok := b.mounts[tag]
	if ok {
		delete(b.mounts, tag)
	}
	return m, ok
}

// removeMountIf drops the share under tag only if it is exactly m. This is the
// rollback path's identity check: a failed attach must undo only the entry it
// added, never a different live share that already holds the tag — otherwise a
// concurrent winner gets clobbered and its host/guest share is orphaned.
func (b *Broker) removeMountIf(tag string, m mountInfo) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cur, ok := b.mounts[tag]; ok && cur == m {
		delete(b.mounts, tag)
		return true
	}
	return false
}

// hasMount reports whether a share with this tag is attached.
func (b *Broker) hasMount(tag string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.mounts[tag]
	return ok
}

// vmOpLock returns the per-VM mutex that serializes attach/detach for id (see
// Broker.opLocks). Held for the whole control sequence; the mounts-map mu is
// taken only briefly inside, so the lock order stays opLock → mu.
func (b *Broker) vmOpLock(id string) *sync.Mutex {
	b.opmu.Lock()
	defer b.opmu.Unlock()
	mu, ok := b.opLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		b.opLocks[id] = mu
	}
	return mu
}

// allocPortLocked returns the lowest free per-session 9p port (caller holds mu).
func (b *Broker) allocPortLocked() uint32 {
	used := make(map[uint32]bool, len(b.mounts))
	for _, m := range b.mounts {
		used[m.port] = true
	}
	for p := vsock.SessionPlan9PortBase; ; p++ {
		if !used[p] {
			return p
		}
	}
}

// Register wires the broker's methods onto the RPC server. The taxonomy mirrors
// Cowork's broker (design.md §8): lifecycle + file passthrough. Only getStatus is
// implemented in this scaffold; the rest are gated, audited stubs.
func (b *Broker) Register(s *rpc.Server) {
	s.Register("getStatus", b.getStatus)
	s.Register("createVM", b.createVM)
	s.Register("startVM", b.startVM)
	s.Register("stopVM", b.stopVM)
	s.Register("exec", b.exec)
	s.Register("execInput", b.execInput)
	s.Register("attachWorkspace", b.attachWorkspace)
	s.Register("detachWorkspace", b.detachWorkspace)
	s.Register("readFile", b.readFile)
	s.Register("writeFile", b.writeFile)
	s.Register("setEgressPolicy", b.setEgressPolicy)
}

// CreateVMParams describes a VM to create. KernelPath/RootFSPath are host paths
// (dev-only; the broker will resolve a pinned bundle itself once it exists).
type CreateVMParams struct {
	ID         string `json:"id"`
	KernelPath string `json:"kernelPath"`
	InitrdPath string `json:"initrdPath"`
	RootFSPath string `json:"rootfsPath"`
	// GuestdImagePath is the host path to the guestd volume (its own ro image, attached
	// as a second disk and mounted by init.sh). guestd is not baked into the rootfs, so
	// this is its sole delivery path; the desktop/vmctl always set it.
	GuestdImagePath string `json:"guestdImagePath"`
	MemoryMB        uint64 `json:"memoryMB"`
	CPUCount        int32  `json:"cpuCount"`
}

// VMRef identifies an existing VM.
type VMRef struct {
	ID string `json:"id"`
}

func (b *Broker) createVM(ctx context.Context, params json.RawMessage) (any, error) {
	if err := b.authorize(ctx, "createVM", "compute"); err != nil {
		return nil, err
	}
	var p CreateVMParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "createVM: " + err.Error()}
	}
	if err := b.vms.Create(ctx, vmm.VMConfig{
		ID:              p.ID,
		KernelPath:      p.KernelPath,
		InitrdPath:      p.InitrdPath,
		RootFSPath:      p.RootFSPath,
		GuestdImagePath: p.GuestdImagePath,
		MemoryMB:        p.MemoryMB,
		CPUCount:        p.CPUCount,
	}); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *Broker) startVM(ctx context.Context, params json.RawMessage) (any, error) {
	if err := b.authorize(ctx, "startVM", "compute"); err != nil {
		return nil, err
	}
	var p VMRef
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "startVM: " + err.Error()}
	}
	if err := b.vms.Start(ctx, p.ID); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *Broker) stopVM(ctx context.Context, params json.RawMessage) (any, error) {
	if err := b.authorize(ctx, "stopVM", "compute"); err != nil {
		return nil, err
	}
	var p VMRef
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "stopVM: " + err.Error()}
	}
	if err := b.vms.Stop(ctx, p.ID); err != nil {
		return nil, err
	}
	return nil, nil
}

// ExecParams asks to run a command inside VM ID. Args/Cwd/Env are optional.
// SessionID, when set, registers the child's stdin in the guest so execInput can
// feed later turns into a long-lived loop (S6.1); empty = legacy one-shot exec.
type ExecParams struct {
	ID        string            `json:"id"`
	Cmd       string            `json:"cmd"`
	SessionID string            `json:"sessionId,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// ExecInputParams pushes a base64 chunk into a running exec session's stdin
// (S6.1): the host feeds a new user turn (or control message) into a persistent
// in-guest loop identified by SessionID.
type ExecInputParams struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Data      string `json:"data"`
}

// ExecResult is the command's exit status. stdout/stderr are streamed before it
// as exec/output notifications, not returned here.
type ExecResult struct {
	ExitCode int `json:"exitCode"`
}

// exec is the host half of Hop 3 (design.md §8): gate the request, dial the
// guest daemon over hvsock, call its exec, and relay each exec/output
// notification straight back to the Hop-2 caller (vmctl) before returning the
// exit code. The connection is per-call (opened and closed around exec).
func (b *Broker) exec(ctx context.Context, params json.RawMessage) (any, error) {
	if err := b.authorize(ctx, "exec", "compute"); err != nil {
		return nil, err
	}
	var p ExecParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "exec: " + err.Error()}
	}
	if p.Cmd == "" {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "exec: cmd is required"}
	}

	conn, err := b.vms.DialGuest(ctx, p.ID)
	if err != nil {
		// DialGuest errors already carry "vm: ..." context; don't re-prefix.
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: err.Error()}
	}
	defer conn.Close()

	// The handler runs on the Hop-2 connection, so this notifier writes back to
	// the caller; relay the guest's exec/output notifications through it verbatim.
	host, _ := rpc.NotifierFromContext(ctx)

	gc := rpc.NewClient(conn)
	var res ExecResult
	err = gc.CallStream(ctx, "exec",
		map[string]any{"cmd": p.Cmd, "sessionId": p.SessionID, "args": p.Args, "cwd": p.Cwd, "env": p.Env},
		&res,
		func(method string, np json.RawMessage) {
			if host != nil {
				_ = host.Notify(method, np)
			}
		})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// execInput forwards a stdin chunk to a persistent in-guest exec session (S6.1).
// The streaming exec holds its own connection, so this opens a fresh, short-lived
// guest connection to deliver the input out-of-band.
func (b *Broker) execInput(ctx context.Context, params json.RawMessage) (any, error) {
	if err := b.authorize(ctx, "execInput", "compute"); err != nil {
		return nil, err
	}
	var p ExecInputParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "execInput: " + err.Error()}
	}
	if p.SessionID == "" {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "execInput: sessionId is required"}
	}

	conn, err := b.vms.DialGuest(ctx, p.ID)
	if err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: err.Error()}
	}
	defer conn.Close()

	return nil, rpc.NewClient(conn).Call(ctx, "execInput",
		map[string]any{"sessionId": p.SessionID, "data": p.Data}, nil)
}

// Status is the getStatus result.
type Status struct {
	Service  string `json:"service"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	PID      int    `json:"pid"`
	UptimeMS int64  `json:"uptimeMs"`
	VMCount  int    `json:"vmCount"`
}

func (b *Broker) getStatus(_ context.Context, _ json.RawMessage) (any, error) {
	return Status{
		Service:  "atelier-host",
		Version:  Version,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		PID:      os.Getpid(),
		UptimeMS: time.Since(b.start).Milliseconds(),
		VMCount:  b.vms.Count(),
	}, nil
}

// authorize is the containment chokepoint: it runs the policy gate and audit log
// for one method/door, returning a wire error if the gate denies it.
func (b *Broker) authorize(ctx context.Context, method, door string) error {
	decision, err := b.gate.Check(ctx, method, door)
	if err != nil {
		return &rpc.Error{Code: rpc.CodeInternal, Message: "policy error: " + err.Error()}
	}
	b.log.Info("rpc", "method", method, "door", door, "decision", decision.String())
	if decision == Deny {
		return &rpc.Error{Code: rpc.CodeInvalidRequest, Message: method + " denied by policy"}
	}
	return nil
}
