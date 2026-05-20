package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
)

func TestFramingRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := Request{JSONRPC: Version, ID: json.RawMessage("1"), Method: "ping"}
	if err := writeMessage(&buf, &in); err != nil {
		t.Fatalf("write: %v", err)
	}
	var out Request
	if err := readMessage(bufio.NewReader(&buf), &out); err != nil {
		t.Fatalf("read: %v", err)
	}
	if out.Method != "ping" || string(out.ID) != "1" {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestServerClientCall(t *testing.T) {
	c1, c2 := net.Pipe()
	srv := NewServer(nil)
	srv.Register("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		return json.RawMessage(params), nil
	})
	go srv.serveConn(context.Background(), c2)

	client := NewClient(c1)
	defer client.Close()

	var got string
	if err := client.Call(context.Background(), "echo", "hello", &got); err != nil {
		t.Fatalf("call: %v", err)
	}
	if got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestServerMethodNotFound(t *testing.T) {
	c1, c2 := net.Pipe()
	srv := NewServer(nil)
	go srv.serveConn(context.Background(), c2)

	client := NewClient(c1)
	defer client.Close()

	err := client.Call(context.Background(), "nope", nil, nil)
	var rpcErr *Error
	if err == nil || !asError(err, &rpcErr) || rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("want method-not-found error, got %v", err)
	}
}

// asError is a tiny errors.As helper kept local to avoid importing errors twice.
func asError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}
