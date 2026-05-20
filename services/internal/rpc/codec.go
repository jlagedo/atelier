package rpc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
)

// writeMessage writes v as a Content-Length framed JSON message (LSP/DAP style).
// Length framing survives embedded newlines in file content / streamed stdout,
// unlike newline-delimited JSON (design.md §8).
func writeMessage(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// readMessage reads one Content-Length framed JSON message into v.
func readMessage(r *bufio.Reader, v any) error {
	headers, err := textproto.NewReader(r).ReadMIMEHeader()
	if err != nil {
		return err
	}
	cl := headers.Get("Content-Length")
	if cl == "" {
		return fmt.Errorf("rpc: missing Content-Length header")
	}
	n, err := strconv.Atoi(cl)
	if err != nil {
		return fmt.Errorf("rpc: invalid Content-Length %q: %w", cl, err)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
