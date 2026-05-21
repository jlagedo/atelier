//go:build !linux

package main

import "log/slog"

// superviseEgress is a no-op off Linux so the daemon still builds on the Windows
// dev box (the guest network forwarder is Linux-only). See egress_linux.go.
func superviseEgress(*slog.Logger) {}
