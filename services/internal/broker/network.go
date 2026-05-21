package broker

import (
	"context"
	"encoding/json"

	"github.com/jlagedo/atelier/services/internal/rpc"
)

// The Network door (design.md §10): the guest reaches only allowlisted
// destinations; everything else is blocked and audited. The broker mediates the
// policy itself (host-side, at the privileged boundary) — setEgressPolicy updates
// the live allowlist the guest's user-mode network (gvisor) consults, so policy
// changes take effect with no VM reboot (the runtime-attach discipline from the
// Files door, S3.1). Default is deny-all (fail-closed); the per-connection
// allow/deny decisions are audited inside the allowlist (door=network).

// SetEgressPolicyParams replaces the egress allowlist with Allow (host suffixes,
// e.g. "pypi.org"). An empty list closes the door (deny all).
type SetEgressPolicyParams struct {
	Allow []string `json:"allow"`
}

func (b *Broker) setEgressPolicy(ctx context.Context, params json.RawMessage) (any, error) {
	if err := b.authorize(ctx, "setEgressPolicy", "network"); err != nil {
		return nil, err
	}
	var p SetEgressPolicyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: err.Error()}
	}
	b.egress.Set(p.Allow)
	return nil, nil
}
