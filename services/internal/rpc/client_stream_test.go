package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// TestCallStreamDeliversNotificationsThenResult proves the client half of the
// streaming path: CallStream hands each interleaved notification to onNotify, in
// order, and then returns the final result — the plumbing the host exec bridge
// (broker→guest and vmctl→broker) relies on.
func TestCallStreamDeliversNotificationsThenResult(t *testing.T) {
	srv := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.Register("exec", func(ctx context.Context, _ json.RawMessage) (any, error) {
		n, _ := NotifierFromContext(ctx)
		_ = n.Notify("exec/output", map[string]string{"stream": "stdout", "data": "one\n"})
		_ = n.Notify("exec/output", map[string]string{"stream": "stderr", "data": "two\n"})
		return map[string]int{"exitCode": 7}, nil
	})

	cConn, sConn := net.Pipe()
	go srv.serveConn(context.Background(), sConn)

	c := NewClient(cConn)
	defer c.Close()
	_ = cConn.SetDeadline(time.Now().Add(5 * time.Second))

	type out struct {
		Stream string `json:"stream"`
		Data   string `json:"data"`
	}
	var got []out
	var res struct {
		ExitCode int `json:"exitCode"`
	}
	err := c.CallStream(context.Background(), "exec", nil, &res, func(method string, params json.RawMessage) {
		if method != "exec/output" {
			t.Errorf("notification method = %q, want exec/output", method)
			return
		}
		var o out
		if err := json.Unmarshal(params, &o); err != nil {
			t.Errorf("decode notification: %v", err)
			return
		}
		got = append(got, o)
	})
	if err != nil {
		t.Fatalf("CallStream: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d notifications, want 2: %+v", len(got), got)
	}
	if got[0].Stream != "stdout" || got[0].Data != "one\n" {
		t.Errorf("notification 0 = %+v, want {stdout one}", got[0])
	}
	if got[1].Stream != "stderr" || got[1].Data != "two\n" {
		t.Errorf("notification 1 = %+v, want {stderr two}", got[1])
	}
	if res.ExitCode != 7 {
		t.Errorf("exitCode = %d, want 7", res.ExitCode)
	}
}

// TestCallStreamCancelAbortsAndTearsDownHandler proves the disconnect-teardown
// path both ends rely on: cancelling the caller's context makes CallStream return
// context.Canceled, and the resulting connection drop cancels the still-running
// server handler (the analog of killing the guest child when the Hop-2 caller
// goes away).
func TestCallStreamCancelAbortsAndTearsDownHandler(t *testing.T) {
	started := make(chan struct{})
	returned := make(chan struct{})
	srv := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.Register("hang", func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(started)
		defer close(returned)
		<-ctx.Done() // unblocks only when the connection is torn down
		return nil, ctx.Err()
	})

	cConn, sConn := net.Pipe()
	go srv.serveConn(context.Background(), sConn)

	c := NewClient(cConn)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.CallStream(ctx, "hang", nil, nil, nil) }()

	<-started // handler is running; now drop the caller
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CallStream err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CallStream did not return after cancel")
	}

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("server handler was not cancelled after the connection dropped")
	}
}
