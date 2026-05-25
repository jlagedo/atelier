package broker

import (
	"context"
	"encoding/json"

	"github.com/jlagedo/atelier/services/internal/rpc"
)

// SetTimeParams names the VM whose clock to seed. There is no timestamp on the
// wire: the broker is the time source (it and the caller share the host clock),
// so callers ask only "sync this VM" and the broker stamps the actual time on the
// Go-only Hop-3 call to runner.
type SetTimeParams struct {
	ID string `json:"id"`
}

// setTime pushes the host wall clock into the guest. The slim virtual-hwe kernel
// has no RTC and VZ offers no time-sync, so without this the guest sits at 1970
// and the agent's TLS to the model fails. The manager also seeds on boot + every
// 30s; this door is the explicit pre-agent seed (and the e2e probe).
func (b *Broker) setTime(ctx context.Context, params json.RawMessage) (any, error) {
	if err := b.authorize(ctx, "setTime", "compute"); err != nil {
		return nil, err
	}
	var p SetTimeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "setTime: " + err.Error()}
	}
	if p.ID == "" {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "setTime: id is required"}
	}
	if err := b.vms.SeedTime(ctx, p.ID); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: err.Error()}
	}
	return nil, nil
}
