package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// frame is a permissive view of any JSON-RPC message on the wire: a
// notification (method, no id), a request (method + id), or a response
// (id + result/error). The test inspects which fields are populated.
type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// TestHandlerNotificationsThenResponse proves a handler can stream notifications
// over the same connection while it runs, and that they arrive before the final
// response, in order — the plumbing runner's exec method relies on.
func TestHandlerNotificationsThenResponse(t *testing.T) {
	srv := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.Register("exec", func(ctx context.Context, _ json.RawMessage) (any, error) {
		n, ok := NotifierFromContext(ctx)
		if !ok {
			t.Error("handler: no notifier in context")
			return nil, &Error{Code: CodeInternal, Message: "no notifier"}
		}
		if err := n.Notify("exec/output", map[string]string{"stream": "stdout", "data": "hello\n"}); err != nil {
			t.Errorf("notify stdout: %v", err)
		}
		if err := n.Notify("exec/output", map[string]string{"stream": "stderr", "data": "warn\n"}); err != nil {
			t.Errorf("notify stderr: %v", err)
		}
		return map[string]int{"exitCode": 0}, nil
	})

	client, server := net.Pipe()
	defer client.Close()
	go srv.serveConn(context.Background(), server)

	_ = client.SetDeadline(time.Now().Add(5 * time.Second))

	idRaw, _ := json.Marshal(1)
	if err := writeMessage(client, &Request{JSONRPC: Version, ID: idRaw, Method: "exec"}); err != nil {
		t.Fatalf("write request: %v", err)
	}

	br := bufio.NewReader(client)
	var frames []frame
	for i := 0; i < 3; i++ {
		var f frame
		if err := readMessage(br, &f); err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		frames = append(frames, f)
	}

	// First two are notifications (method set, no id); third is the response.
	for i := 0; i < 2; i++ {
		if frames[i].Method != "exec/output" {
			t.Errorf("frame %d: method = %q, want exec/output", i, frames[i].Method)
		}
		if len(frames[i].ID) != 0 {
			t.Errorf("frame %d: notification must have no id, got %s", i, frames[i].ID)
		}
	}
	var p0 struct{ Stream, Data string }
	_ = json.Unmarshal(frames[0].Params, &p0)
	if p0.Stream != "stdout" || p0.Data != "hello\n" {
		t.Errorf("frame 0 params = %+v, want {stdout hello}", p0)
	}

	resp := frames[2]
	if len(resp.ID) == 0 {
		t.Errorf("frame 2: response must carry the request id")
	}
	if resp.Error != nil {
		t.Fatalf("frame 2: unexpected error %+v", resp.Error)
	}
	var res struct{ ExitCode int }
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exitCode = %d, want 0", res.ExitCode)
	}
}
