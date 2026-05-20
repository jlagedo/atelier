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
	br := bufio.NewReader(conn)
	for {
		var req Request
		if err := readMessage(br, &req); err != nil {
			if !errors.Is(err, io.EOF) {
				s.log.Debug("rpc read", "err", err)
			}
			return
		}
		s.dispatch(ctx, conn, &req)
	}
}

func (s *Server) dispatch(ctx context.Context, w io.Writer, req *Request) {
	h, ok := s.handler(req.Method)
	if !ok {
		if !req.IsNotification() {
			s.writeError(w, req.ID, &Error{Code: CodeMethodNotFound, Message: "method not found: " + req.Method})
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
		s.writeError(w, req.ID, rpcErr)
		return
	}

	raw, err := json.Marshal(result)
	if err != nil {
		s.writeError(w, req.ID, &Error{Code: CodeInternal, Message: "marshal result: " + err.Error()})
		return
	}
	_ = writeMessage(w, &Response{JSONRPC: Version, ID: req.ID, Result: raw})
}

func (s *Server) writeError(w io.Writer, id json.RawMessage, e *Error) {
	_ = writeMessage(w, &Response{JSONRPC: Version, ID: id, Error: e})
}
