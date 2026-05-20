package rpc

import "context"

// Notifier lets a handler push JSON-RPC notifications (server→client messages
// with no id) back over the same connection while it is still running. This is
// how streamed output is carried (design.md §8: "Streaming = JSON-RPC
// notifications … one channel, no second socket") — e.g. guestd's exec method
// emits stdout/stderr as notifications, then returns the final result.
type Notifier interface {
	Notify(method string, params any) error
}

type notifierKey struct{}

// WithNotifier attaches a Notifier to ctx. The server does this per connection
// before dispatching, so handlers can retrieve it with NotifierFromContext.
func WithNotifier(ctx context.Context, n Notifier) context.Context {
	return context.WithValue(ctx, notifierKey{}, n)
}

// NotifierFromContext returns the Notifier attached by the server, if any.
// Handlers that don't stream can ignore it.
func NotifierFromContext(ctx context.Context) (Notifier, bool) {
	n, ok := ctx.Value(notifierKey{}).(Notifier)
	return n, ok
}
