//go:build !windows

package vm

import "log/slog"

// noopConsole is the non-Windows placeholder: there is no HCS to bridge to, so
// the console stream does nothing. The HCS driver itself returns ErrUnsupported.
type noopConsole struct{}

func newConsoleStream(_ string, _ *slog.Logger) (consoleStream, error) {
	return noopConsole{}, nil
}

func (noopConsole) Close() error { return nil }
