//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// hasSeq reports whether sub appears as a contiguous run inside args.
func hasSeq(args []string, sub ...string) bool {
	for i := 0; i+len(sub) <= len(args); i++ {
		if slices.Equal(args[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

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

func TestSandboxedCommandNarrowsBind(t *testing.T) {
	blob := filepath.Join(t.TempDir(), "seccomp.bpf")
	if err := os.WriteFile(blob, []byte("bpf"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := seccompFilterPath
	seccompFilterPath = blob
	t.Cleanup(func() { seccompFilterPath = old })

	cmd, err := sandboxedCommand(context.Background(), execParams{
		Cmd:       "/opt/atelier/packages/artisan/node_modules/.bin/tsx",
		Args:      []string{"src/cli-guest.ts", "--serve", "--workspace", "/sessions/s123"},
		Cwd:       "/opt/atelier/packages/artisan", // covered by the ro toolbox
		SessionID: "stdin-chan",                    // only the stdin channel — NOT a bind source
	})
	if err != nil {
		t.Fatalf("sandboxedCommand: %v", err)
	}
	t.Cleanup(func() { _ = cmd.ExtraFiles[0].Close() })
	a := cmd.Args

	// The whole-rootfs bind is gone (F-03/F-09 closed by construction).
	if hasSeq(a, "--bind", "/", "/") {
		t.Errorf("narrowed sandbox must not contain `--bind / /`: %v", a)
	}
	// Curated read-only toolbox + sized scratch + HOME.
	for _, want := range [][]string{
		{"--ro-bind", "/usr", "/usr"},
		{"--ro-bind", "/opt/atelier", "/opt/atelier"},
		{"--tmpfs", "/tmp"},
		{"--tmpfs", "/run"},
		{"--bind", "/home/atelier", "/home/atelier"},
	} {
		if !hasSeq(a, want...) {
			t.Errorf("missing %v in args: %v", want, a)
		}
	}
	// The runner volume (binary + seccomp blob) is never bound into the sandbox.
	for _, tok := range a {
		if strings.Contains(tok, "/opt/runner") {
			t.Errorf("sandbox must not reference /opt/runner: %v", a)
		}
	}
	// The workspace (from --workspace) is the rw real-storage path, bound tolerantly; the
	// cwd under /opt/atelier is NOT separately bound (already covered, read-only).
	if !hasSeq(a, "--bind-try", "/sessions/s123", "/sessions/s123") {
		t.Errorf("missing workspace rw bind: %v", a)
	}
	// SessionID names only the stdin channel — it must never become a bind source.
	if hasSeq(a, "--bind-try", "/sessions/stdin-chan", "/sessions/stdin-chan") ||
		hasSeq(a, "--bind", "/sessions/stdin-chan", "/sessions/stdin-chan") {
		t.Errorf("SessionID must not be bound as a path: %v", a)
	}
	if hasSeq(a, "--bind", "/opt/atelier/packages/artisan", "/opt/atelier/packages/artisan") {
		t.Errorf("cwd under /opt/atelier should not get its own bind: %v", a)
	}
	// The Landlock shim is the exec target after `--seccomp 3 --`, gets the rw path, then
	// the real command follows its own `--` separator.
	i := slices.Index(a, "--seccomp")
	if i < 0 || i+3 >= len(a) || a[i+1] != "3" || a[i+2] != "--" || a[i+3] != landlockShim {
		t.Fatalf("expected `--seccomp 3 -- <shim>` tail: %v", a)
	}
	if !hasSeq(a, "--rw", "/sessions/s123") {
		t.Errorf("shim missing --rw for the session workspace: %v", a)
	}
	if !hasSeq(a, "--", "/opt/atelier/packages/artisan/node_modules/.bin/tsx", "src/cli-guest.ts") {
		t.Errorf("real command not found after the shim separator: %v", a)
	}
}

func TestCwdNeedsBind(t *testing.T) {
	cases := map[string]bool{
		"":                              false,
		"/":                             false,
		"relative/path":                 false,
		"/usr":                          false,
		"/opt/atelier/packages/artisan": false,
		"/home/atelier/.cache":          false,
		"/tmp/x":                        false,
		"/sessions/s1":                  true,
		"/workspace":                    true,
		"/mnt/proj":                     true,
	}
	for in, want := range cases {
		if got := cwdNeedsBind(in); got != want {
			t.Errorf("cwdNeedsBind(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestWorkspaceArg(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"src/cli-guest.ts", "--serve", "--workspace", "/sessions/x"}, "/sessions/x"},
		{[]string{"--workspace=/sessions/y"}, "/sessions/y"},
		{[]string{"src/cli-guest.ts", "--task", "do it"}, ""},
		{[]string{"--workspace"}, ""}, // dangling flag, no value
	}
	for _, c := range cases {
		if got := workspaceArg(c.args); got != c.want {
			t.Errorf("workspaceArg(%v) = %q, want %q", c.args, got, c.want)
		}
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
