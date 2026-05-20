package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
)

// HandlerFunc handles a single RPC method. Returning a *Error sets the wire
// error code/message; any other error becomes a generic internal error.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, error)

// Server is a JSON-RPC 2.0 server over Content-Length framed connections.
type Server struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
	log      *slog.Logger
}

// NewServer returns a Server. A nil logger uses slog.Default().
func NewServer(log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{handlers: make(map[string]HandlerFunc), log: log}
}

// Register associates a handler with a method name.
func (s *Server) Register(method string, h HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

func (s *Server) handler(method string) (HandlerFunc, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.handlers[method]
	return h, ok
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.serveConn(ctx, conn)
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	// Tie a cancelable context to the connection's lifetime: when the peer
	// disconnects (readMessage below returns), defer cancel() aborts any handler
	// still in flight. That tears down a long-running exec — and kills its guest
	// child via exec.CommandContext — instead of leaking it after the caller goes
	// away.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// One writer per connection: serializes whole messages so a handler's
	// streamed notifications can't interleave with each other or the final
	// response (the exec handler streams stdout+stderr from two goroutines).
	cw := &connWriter{w: conn}
	ctx = WithNotifier(ctx, cw)
	br := bufio.NewReader(conn)
	for {
		var req Request
		if err := readMessage(br, &req); err != nil {
			if !errors.Is(err, io.EOF) {
				s.log.Debug("rpc read", "err", err)
			}
			return
		}
		// Dispatch in a goroutine so the read loop keeps watching the connection
		// while a handler runs; a mid-flight disconnect then unblocks readMessage
		// and cancels the handler. req is a fresh value each iteration, so taking
		// its address here is safe.
		go s.dispatch(ctx, cw, &req)
	}
}

func (s *Server) dispatch(ctx context.Context, cw *connWriter, req *Request) {
	h, ok := s.handler(req.Method)
	if !ok {
		if !req.IsNotification() {
			s.writeError(cw, req.ID, &Error{Code: CodeMethodNotFound, Message: "method not found: " + req.Method})
		}
		return
	}

	result, err := h(ctx, req.Params)
	if req.IsNotification() {
		return // notifications never get a response, even on error
	}
	if err != nil {
		var rpcErr *Error
		if !errors.As(err, &rpcErr) {
			rpcErr = &Error{Code: CodeInternal, Message: err.Error()}
		}
		s.writeError(cw, req.ID, rpcErr)
		return
	}

	raw, err := json.Marshal(result)
	if err != nil {
		s.writeError(cw, req.ID, &Error{Code: CodeInternal, Message: "marshal result: " + err.Error()})
		return
	}
	_ = cw.writeMsg(&Response{JSONRPC: Version, ID: req.ID, Result: raw})
}

func (s *Server) writeError(cw *connWriter, id json.RawMessage, e *Error) {
	_ = cw.writeMsg(&Response{JSONRPC: Version, ID: id, Error: e})
}

// connWriter serializes Content-Length framed messages to one connection. Each
// writeMsg call holds the lock for the whole message (header + body), so
// concurrent notifications and the final response never corrupt the framing.
type connWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (c *connWriter) writeMsg(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return writeMessage(c.w, v)
}

// Notify implements Notifier: a JSON-RPC notification is a request with no ID.
func (c *connWriter) Notify(method string, params any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	return c.writeMsg(&Request{JSONRPC: Version, Method: method, Params: raw})
}
