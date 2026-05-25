package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
)

// Client is a minimal synchronous JSON-RPC client (one in-flight call at a time),
// enough to drive the host from atelierctl. A pipelined client comes later if needed.
type Client struct {
	conn net.Conn
	br   *bufio.Reader
	id   atomic.Int64
}

// NewClient wraps a connection in a client.
func NewClient(conn net.Conn) *Client {
	return &Client{conn: conn, br: bufio.NewReader(conn)}
}

// Close closes the underlying connection.
func (c *Client) Close() error { return c.conn.Close() }

// Call sends a request and decodes the response result into result (if non-nil).
func (c *Client) Call(_ context.Context, method string, params, result any) error {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		rawParams = b
	}

	idRaw, _ := json.Marshal(c.id.Add(1))
	req := Request{JSONRPC: Version, ID: idRaw, Method: method, Params: rawParams}
	if err := writeMessage(c.conn, &req); err != nil {
		return err
	}

	var resp Response
	if err := readMessage(c.br, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	if result != nil && len(resp.Result) > 0 {
		return json.Unmarshal(resp.Result, result)
	}
	return nil
}

// streamFrame is a permissive view of any message read while a CallStream is in
// flight: a notification (method, no id) or the response (id + result/error).
type streamFrame struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// CallStream sends a request and invokes onNotify for each JSON-RPC notification
// that arrives on the connection before the matching response. It returns when
// the response (or an error response) is read. This is the client half of the
// server's streaming notifications (design.md §8): exec streams stdout/stderr as
// notifications, then returns its result. onNotify may be nil.
func (c *Client) CallStream(ctx context.Context, method string, params, result any, onNotify func(method string, params json.RawMessage)) error {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		rawParams = b
	}

	idRaw, _ := json.Marshal(c.id.Add(1))
	req := Request{JSONRPC: Version, ID: idRaw, Method: method, Params: rawParams}
	if err := writeMessage(c.conn, &req); err != nil {
		return err
	}

	// Honor context cancellation during the (otherwise blocking) read loop: a
	// cancel closes the connection, which unblocks readMessage. The broker drives
	// the guest exec with its per-connection context, so when the Hop-2 caller
	// disconnects this is what aborts the guest-side stream.
	if done := ctx.Done(); done != nil {
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			select {
			case <-done:
				_ = c.conn.Close()
			case <-stop:
			}
		}()
	}

	for {
		var f streamFrame
		if err := readMessage(c.br, &f); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		// A notification has a method and no id; deliver it and keep reading.
		if f.Method != "" && len(f.ID) == 0 {
			if onNotify != nil {
				onNotify(f.Method, f.Params)
			}
			continue
		}
		// Otherwise this is the response to our request.
		if f.Error != nil {
			return f.Error
		}
		if result != nil && len(f.Result) > 0 {
			return json.Unmarshal(f.Result, result)
		}
		return nil
	}
}
