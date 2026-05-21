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

	"github.com/jlagedo/atelier/services/internal/rpc"
	"github.com/jlagedo/atelier/services/internal/vm"
)

// Version is the host service version.
const Version = "0.0.0"

// Broker holds the policy gate, audit logger, VM manager, and process start time.
type Broker struct {
	start time.Time
	log   *slog.Logger
	gate  Gate
	vms   *vm.Manager

	// mu guards workspace, the canonicalized host folder currently attached as the
	// Files-door root (design.md §10 — S3.1): the jail root readFile/writeFile
	// resolve against, set by attachWorkspace and cleared by detachWorkspace.
	// Empty means no workspace is attached (the door is closed). A single root
	// today; this generalizes to a per-session map later.
	mu        sync.Mutex
	workspace string
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
	return &Broker{start: time.Now(), log: log, gate: gate, vms: vm.NewManager(log)}
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

// Register wires the broker's methods onto the RPC server. The taxonomy mirrors
// Cowork's broker (design.md §8): lifecycle + file passthrough. Only getStatus is
// implemented in this scaffold; the rest are gated, audited stubs.
func (b *Broker) Register(s *rpc.Server) {
	s.Register("getStatus", b.getStatus)
	s.Register("createVM", b.createVM)
	s.Register("startVM", b.startVM)
	s.Register("stopVM", b.stopVM)
	s.Register("exec", b.exec)
	s.Register("attachWorkspace", b.attachWorkspace)
	s.Register("detachWorkspace", b.detachWorkspace)
	s.Register("readFile", b.readFile)
	s.Register("writeFile", b.writeFile)
}

// CreateVMParams describes a VM to create. KernelPath/RootFSPath are host paths
// (dev-only; the broker will resolve a pinned bundle itself once it exists).
type CreateVMParams struct {
	ID         string `json:"id"`
	KernelPath string `json:"kernelPath"`
	InitrdPath string `json:"initrdPath"`
	RootFSPath string `json:"rootfsPath"`
	MemoryMB   uint64 `json:"memoryMB"`
	CPUCount   int32  `json:"cpuCount"`
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
	if err := b.vms.Create(ctx, vm.VMConfig{
		ID:         p.ID,
		KernelPath: p.KernelPath,
		InitrdPath: p.InitrdPath,
		RootFSPath: p.RootFSPath,
		MemoryMB:   p.MemoryMB,
		CPUCount:   p.CPUCount,
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
type ExecParams struct {
	ID   string            `json:"id"`
	Cmd  string            `json:"cmd"`
	Args []string          `json:"args,omitempty"`
	Cwd  string            `json:"cwd,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
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
		map[string]any{"cmd": p.Cmd, "args": p.Args, "cwd": p.Cwd, "env": p.Env},
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
