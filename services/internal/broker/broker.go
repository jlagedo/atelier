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
	"time"

	"github.com/jlagedo/atelier/services/internal/rpc"
)

// Version is the host service version.
const Version = "0.0.0"

// Broker holds the policy gate, audit logger, and process start time.
type Broker struct {
	start time.Time
	log   *slog.Logger
	gate  Gate
}

// New returns a Broker. A nil logger uses slog.Default(); a nil gate uses the
// dev-time AllowAll gate.
func New(log *slog.Logger, gate Gate) *Broker {
	if log == nil {
		log = slog.Default()
	}
	if gate == nil {
		gate = AllowAll{log: log}
	}
	return &Broker{start: time.Now(), log: log, gate: gate}
}

// Register wires the broker's methods onto the RPC server. The taxonomy mirrors
// Cowork's broker (design.md §8): lifecycle + file passthrough. Only getStatus is
// implemented in this scaffold; the rest are gated, audited stubs.
func (b *Broker) Register(s *rpc.Server) {
	s.Register("getStatus", b.getStatus)
	s.Register("createVM", b.gatedStub("createVM", "compute"))
	s.Register("startVM", b.gatedStub("startVM", "compute"))
	s.Register("stopVM", b.gatedStub("stopVM", "compute"))
	s.Register("readFile", b.gatedStub("readFile", "files"))
	s.Register("writeFile", b.gatedStub("writeFile", "files"))
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
		VMCount:  0,
	}, nil
}

// gatedStub returns a handler that runs the policy gate + audit log (the
// containment chokepoint) before reporting that the method isn't built yet.
func (b *Broker) gatedStub(method, door string) rpc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		decision, err := b.gate.Check(ctx, method, door)
		if err != nil {
			return nil, &rpc.Error{Code: rpc.CodeInternal, Message: "policy error: " + err.Error()}
		}
		b.log.Info("rpc", "method", method, "door", door, "decision", decision.String())
		if decision == Deny {
			return nil, &rpc.Error{Code: rpc.CodeInvalidRequest, Message: method + " denied by policy"}
		}
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: method + " not implemented yet (scaffold)"}
	}
}
