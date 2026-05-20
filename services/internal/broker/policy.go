package broker

import (
	"context"
	"log/slog"
)

// Decision is the outcome of a policy check (design.md §2, §10).
type Decision int

const (
	Deny Decision = iota
	Ask
	Allow
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Ask:
		return "ask"
	default:
		return "deny"
	}
}

// Gate is the containment chokepoint: every capability use is allow/ask/deny and
// audited. The agent has no ambient authority — it only acts through this gate.
type Gate interface {
	Check(ctx context.Context, action, door string) (Decision, error)
}

// AllowAll is the dev-time gate: everything is allowed, but still logged. Real
// policy (per-door allow/ask/deny + an approval UI) arrives with later milestones.
type AllowAll struct{ log *slog.Logger }

// Check implements Gate.
func (g AllowAll) Check(_ context.Context, action, door string) (Decision, error) {
	if g.log != nil {
		g.log.Debug("policy check", "action", action, "door", door, "decision", "allow")
	}
	return Allow, nil
}
