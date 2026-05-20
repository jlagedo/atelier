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

	srv := rpc.NewServer(log)
	srv.Register("exec", (&guest{log: log}).exec)

	if err := srv.Serve(context.Background(), ln); err != nil {
		log.Error("rpc serve stopped", "err", err)
	}
	log.Warn("guestd serve returned — staying up (PID 1)")
	block()
}

// block parks the (PID 1) goroutine forever. guestd must never return to init.
func block() { select {} }

type guest struct{ log *slog.Logger }

type execParams struct {
	Cmd  string            `json:"cmd"`
	Args []string          `json:"args,omitempty"`
	Cwd  string            `json:"cwd,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
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

	cmd := exec.CommandContext(ctx, p.Cmd, p.Args...)
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
