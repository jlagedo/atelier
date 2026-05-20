package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
)

// Client is a minimal synchronous JSON-RPC client (one in-flight call at a time),
// enough to drive the host from vmctl. A pipelined client comes later if needed.
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
