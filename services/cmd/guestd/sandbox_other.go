//go:build !linux

package main

import (
	"context"
	"os/exec"
)

// sandboxedCommand off Linux just runs the command directly — the bubblewrap sandbox and
// uid drop are Linux-only. guestd only ever runs in the Linux guest, but cmd/guestd must
// still build on the Windows dev box (go build ./...). See sandbox_linux.go.
func sandboxedCommand(ctx context.Context, p execParams) *exec.Cmd {
	return exec.CommandContext(ctx, p.Cmd, p.Args...)
}
