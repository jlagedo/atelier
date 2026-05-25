//go:build !linux

package main

import (
	"log/slog"
	"os/exec"
)

// applyCgroupLimits is a no-op off Linux (the runner only runs in the guest VM); the
// stub keeps `go build ./...` green on the macOS/Windows dev hosts.
func applyCgroupLimits(_ *slog.Logger, _ *exec.Cmd, _ execParams) func() { return func() {} }
