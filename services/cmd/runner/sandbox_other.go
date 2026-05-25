//go:build !linux

package main

import (
	"context"
	"os/exec"
)

// sandboxedCommand off Linux just runs the command directly — the bubblewrap sandbox, uid
// drop, and seccomp filter are Linux-only. runner only ever runs in the Linux guest, but
// cmd/runner must still build on the Windows dev box (go build ./...). See sandbox_linux.go.
func sandboxedCommand(ctx context.Context, p execParams) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, p.Cmd, p.Args...), nil
}
