//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSandboxedCommandInstallsSeccomp(t *testing.T) {
	blob := filepath.Join(t.TempDir(), "seccomp.bpf")
	if err := os.WriteFile(blob, []byte("bpf"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := seccompFilterPath
	seccompFilterPath = blob
	t.Cleanup(func() { seccompFilterPath = old })

	cmd, err := sandboxedCommand(context.Background(), execParams{Cmd: "/bin/echo", Args: []string{"hi"}})
	if err != nil {
		t.Fatalf("sandboxedCommand: %v", err)
	}
	if cmd.Path != bwrapPath {
		t.Errorf("Path = %q, want %q", cmd.Path, bwrapPath)
	}
	// bwrap reads the cBPF program from fd 3 == ExtraFiles[0].
	if i := slices.Index(cmd.Args, "--seccomp"); i < 0 || i+1 >= len(cmd.Args) || cmd.Args[i+1] != "3" {
		t.Errorf("args missing `--seccomp 3`: %v", cmd.Args)
	}
	if len(cmd.ExtraFiles) != 1 {
		t.Fatalf("ExtraFiles len = %d, want 1", len(cmd.ExtraFiles))
	}
	_ = cmd.ExtraFiles[0].Close()
}

func TestSandboxedCommandFailsClosedWithoutFilter(t *testing.T) {
	old := seccompFilterPath
	seccompFilterPath = filepath.Join(t.TempDir(), "does-not-exist.bpf")
	t.Cleanup(func() { seccompFilterPath = old })

	if _, err := sandboxedCommand(context.Background(), execParams{Cmd: "/bin/echo"}); err == nil {
		t.Fatal("expected error when seccomp filter is missing, got nil")
	}
}

func TestSandboxedCommandPrivilegedBypassesSandbox(t *testing.T) {
	// Privileged must not need the filter and must run the command directly (escape hatch).
	old := seccompFilterPath
	seccompFilterPath = filepath.Join(t.TempDir(), "nope.bpf")
	t.Cleanup(func() { seccompFilterPath = old })

	cmd, err := sandboxedCommand(context.Background(), execParams{Cmd: "/bin/echo", Privileged: true})
	if err != nil {
		t.Fatalf("privileged sandboxedCommand: %v", err)
	}
	if cmd.Path != "/bin/echo" {
		t.Errorf("Path = %q, want /bin/echo", cmd.Path)
	}
	if slices.Contains(cmd.Args, "--seccomp") {
		t.Errorf("privileged cmd should not carry --seccomp: %v", cmd.Args)
	}
	if len(cmd.ExtraFiles) != 0 {
		t.Errorf("privileged cmd should have no ExtraFiles, got %d", len(cmd.ExtraFiles))
	}
}
