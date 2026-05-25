// Command guestd is the in-VM daemon: the AF_VSOCK JSON-RPC server side of the
// host's control plane (design.md §8 Hop 3, the model being Cowork's coworkd).
// init.sh execs it as PID 1. It serves one method, exec, which runs a command
// and streams stdout/stderr back as JSON-RPC notifications ("Streaming =
// JSON-RPC notifications" — §8). The host client + the full round-trip land in
// S2.2; for S2.1 the observable is: guestd comes up and listens on the vsock port.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"

	"github.com/jlagedo/atelier/services/internal/rpc"
	"github.com/jlagedo/atelier/services/internal/vsock"
)

func main() {
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	ln, err := vsock.Listen()
	if err != nil {
		// Exiting PID 1 panics the kernel; block instead so the serial console
		// keeps this error readable during bring-up.
		log.Error("vsock listen failed — staying up so the serial log is readable",
			"port", vsock.GuestRPCPort, "err", err)
		block()
	}
	log.Info("atelier-guestd listening", "transport", "vsock", "port", vsock.GuestRPCPort)

	// Bring up the guest's network link (Network door, S4.1): gvforwarder bridges
	// a tap device to the host's user-mode network over vsock. Supervised in the
	// background; egress is still gated host-side by the allowlist (default-deny).
	superviseEgress(log)

	srv := rpc.NewServer(log)
	g := &guest{log: log, stdins: make(map[string]io.WriteCloser)}
	srv.Register("exec", g.exec)
	srv.Register("execInput", g.execInput) // S6.1: feed stdin of a running exec session
	srv.Register("mount", g.mount)         // Files door (S3.1): mount a host 9p share
	srv.Register("unmount", g.unmount)     // Files door (S3.1): unmount a share
	srv.Register("setTime", g.setTime)     // host pushes wall-clock time (no RTC under VZ)

	if err := srv.Serve(context.Background(), ln); err != nil {
		log.Error("rpc serve stopped", "err", err)
	}
	log.Warn("guestd serve returned — staying up (PID 1)")
	block()
}

// block parks the (PID 1) goroutine forever. guestd must never return to init.
func block() { select {} }

type guest struct {
	log *slog.Logger
	// stdins maps an exec session id → the running child's stdin writer (S6.1),
	// so a later execInput call can push input (e.g. a new user turn) into a
	// long-lived process. Guarded by mu.
	mu     sync.Mutex
	stdins map[string]io.WriteCloser
}

func (g *guest) setStdin(id string, w io.WriteCloser) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stdins[id] = w
}

func (g *guest) getStdin(id string) (io.WriteCloser, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w, ok := g.stdins[id]
	return w, ok
}

func (g *guest) delStdin(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.stdins, id)
}

type execParams struct {
	Cmd string `json:"cmd"`
	// SessionID, when set, registers the child's stdin so execInput can feed it
	// (S6.1 persistent loops). Empty = no stdin channel (legacy one-shot exec).
	SessionID string            `json:"sessionId,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	// Privileged skips the bubblewrap sandbox and runs the command directly as root —
	// an operator/debug escape hatch. Default false: every exec is sandboxed as the
	// non-root agent user (CRIT-01), enforced here at the privileged boundary so a client
	// can't opt out.
	Privileged bool `json:"privileged,omitempty"`
}

// execInputParams pushes a chunk to a running exec session's stdin (S6.1). Data is
// base64 (std), matching the binary-safe discipline of exec/output.
type execInputParams struct {
	SessionID string `json:"sessionId"`
	Data      string `json:"data"` // base64-encoded
}

// outputParams is the payload of an "exec/output" notification: a chunk of the
// child's stdout or stderr as it is produced. Data is base64 (std encoding) so
// the stream is binary-safe — raw bytes can't survive a JSON string field
// otherwise (invalid UTF-8 becomes U+FFFD), and a multibyte rune split across a
// read boundary would corrupt too. The host (vmctl) decodes before writing out.
type outputParams struct {
	Stream string `json:"stream"` // "stdout" | "stderr"
	Data   string `json:"data"`   // base64-encoded chunk
}

type execResult struct {
	ExitCode int `json:"exitCode"`
}

// exec runs a command, streaming its output as exec/output notifications, then
// returns the exit code. The notifier comes from the connection (rpc.Server).
func (g *guest) exec(ctx context.Context, raw json.RawMessage) (any, error) {
	var p execParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "bad params: " + err.Error()}
	}
	if p.Cmd == "" {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "cmd is required"}
	}
	notifier, _ := rpc.NotifierFromContext(ctx)

	cmd, err := sandboxedCommand(ctx, p)
	if err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: "sandbox: " + err.Error()}
	}
	// The seccomp blob fd (ExtraFiles[0]) is dup'd into the bwrap child by Start; the parent
	// copy is ours. defer covers every return path (pipe error, start failure, wait).
	if len(cmd.ExtraFiles) > 0 {
		defer func() { _ = cmd.ExtraFiles[0].Close() }()
	}
	if p.Cwd != "" {
		cmd.Dir = p.Cwd
	}
	if len(p.Env) > 0 {
		env := os.Environ()
		for k, v := range p.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: "stdout pipe: " + err.Error()}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: "stderr pipe: " + err.Error()}
	}
	// When a sessionId is given, keep a handle to the child's stdin so execInput
	// can push later turns into a long-lived loop (S6.1). Registered before Start
	// and torn down after Wait.
	if p.SessionID != "" {
		stdin, perr := cmd.StdinPipe()
		if perr != nil {
			return nil, &rpc.Error{Code: rpc.CodeInternal, Message: "stdin pipe: " + perr.Error()}
		}
		g.setStdin(p.SessionID, stdin)
		defer func() {
			g.delStdin(p.SessionID)
			_ = stdin.Close()
		}()
	}
	if err := cmd.Start(); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: "start: " + err.Error()}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go streamPipe(&wg, notifier, "stdout", stdout)
	go streamPipe(&wg, notifier, "stderr", stderr)
	wg.Wait()

	code := 0
	if err := cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			return nil, &rpc.Error{Code: rpc.CodeInternal, Message: "wait: " + err.Error()}
		}
	}
	return execResult{ExitCode: code}, nil
}

// execInput writes a base64 chunk to a running exec session's stdin (S6.1): the
// host pushes a new user turn (or a control message) into a persistent loop.
func (g *guest) execInput(_ context.Context, raw json.RawMessage) (any, error) {
	var p execInputParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "bad params: " + err.Error()}
	}
	if p.SessionID == "" {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "sessionId is required"}
	}
	w, ok := g.getStdin(p.SessionID)
	if !ok {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "no such exec session: " + p.SessionID}
	}
	data, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "data must be base64: " + err.Error()}
	}
	if _, err := w.Write(data); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: "write stdin: " + err.Error()}
	}
	return nil, nil
}

// mountParams asks guestd to mount a host 9p share (Files door, S3.1): dial the
// host on Port and mount it (aname=Tag) at Target.
type mountParams struct {
	Port   uint32 `json:"port"`
	Tag    string `json:"tag"`
	Target string `json:"target"`
}

type unmountParams struct {
	Target string `json:"target"`
}

// mount mounts a host Plan9/9p share the host just added via ModifyComputeSystem.
func (g *guest) mount(_ context.Context, raw json.RawMessage) (any, error) {
	var p mountParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "bad params: " + err.Error()}
	}
	if p.Target == "" || p.Tag == "" || p.Port == 0 {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "port, tag and target are required"}
	}
	if err := mountShare(p.Port, p.Tag, p.Target); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: err.Error()}
	}
	g.log.Info("mounted share", "target", p.Target, "port", p.Port, "tag", p.Tag)
	return nil, nil
}

// unmount unmounts a previously-mounted share (the guest half of detach).
func (g *guest) unmount(_ context.Context, raw json.RawMessage) (any, error) {
	var p unmountParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "bad params: " + err.Error()}
	}
	if p.Target == "" {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "target is required"}
	}
	if err := unmountShare(p.Target); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: err.Error()}
	}
	g.log.Info("unmounted share", "target", p.Target)
	return nil, nil
}

// setTimeParams carries the host's wall-clock time in unix milliseconds. The
// value only ever rides this Go-only Hop-3 call, so it never touches a JS wire
// (no 2^53 concern); the broker is the source and stamps time.Now().UnixMilli().
type setTimeParams struct {
	UnixMs int64 `json:"unixMs"`
}

// setTime steps the guest's CLOCK_REALTIME to the host's wall clock. The slim
// virtual-hwe kernel ships no built-in RTC and VZ offers no time-sync, so without
// this the guest sits at 1970 and TLS to the model fails ("cert not yet valid").
func (g *guest) setTime(_ context.Context, raw json.RawMessage) (any, error) {
	var p setTimeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "bad params: " + err.Error()}
	}
	if p.UnixMs <= 0 {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "unixMs must be positive"}
	}
	if err := setSystemClock(p.UnixMs); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: "set clock: " + err.Error()}
	}
	g.log.Info("clock set", "unixMs", p.UnixMs)
	return nil, nil
}

// streamPipe forwards a child's output stream as exec/output notifications until
// the pipe closes (process exit).
func streamPipe(wg *sync.WaitGroup, n rpc.Notifier, stream string, r io.Reader) {
	defer wg.Done()
	buf := make([]byte, 32*1024)
	for {
		m, err := r.Read(buf)
		if m > 0 && n != nil {
			_ = n.Notify("exec/output", outputParams{Stream: stream, Data: base64.StdEncoding.EncodeToString(buf[:m])})
		}
		if err != nil {
			return
		}
	}
}
