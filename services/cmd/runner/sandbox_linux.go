//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// bwrapPath is the bubblewrap binary baked into the guest image (CRIT-01 sandbox).
const bwrapPath = "/usr/bin/bwrap"

// seccompFilterPath is the precompiled cBPF program bwrap installs for the agent (F-13,
// closing F-01). It rides on the read-only runner volume next to the runner binary, built
// in the image pipeline from Docker's default profile evaluated for a no-capability process
// (image/agent/seccomp). A var, not a const, so tests can point it at a fixture.
var seccompFilterPath = "/opt/runner/seccomp.bpf"

// agentUID/agentGID are the non-root identity the agent runs as (CRIT-01). The image
// creates this user (image/rootfs/Dockerfile) and /home/atelier is a tmpfs chowned to it
// (image/guest/init.sh).
const (
	agentUID = 1001
	agentGID = 1001
)

// Sandbox filesystem policy. The agent gets a curated read-only toolbox plus a small
// set of writable scratch/workspace paths; everything else is absent from its mount
// namespace (no runner binary at /opt/runner — F-03 — and no sibling sessions — F-09).
const (
	tmpfsTmpBytes   = 256 * 1024 * 1024 // /tmp scratch cap
	tmpfsRunBytes   = 64 * 1024 * 1024  // /run scratch cap
	legacyWorkspace = "/workspace"      // single-share workspace (one-shot/dev path)
	// landlockShim is the innermost exec wrapper that applies the Landlock domain before
	// running the command. It ships on the runner volume under the /opt/atelier ro-bind.
	landlockShim = "/opt/atelier/sbin/atelier-landlock"
)

// sandboxOptionalEtcFiles are ro-bound when present (glibc resolver, TLS, identity).
// Bound with --ro-bind-try so a missing file never aborts bwrap.
var sandboxOptionalEtcFiles = []string{
	"/etc/resolv.conf", "/etc/nsswitch.conf", "/etc/passwd", "/etc/group",
	"/etc/localtime", "/etc/hosts", "/etc/host.conf", "/etc/gai.conf",
}

// sandboxCoveredPrefixes are paths already mounted (ro toolbox or rw scratch); an
// ad-hoc cwd under any of them needs no extra bind.
var sandboxCoveredPrefixes = []string{
	"/usr", "/opt/atelier", "/etc", "/bin", "/lib", "/lib64", "/sbin",
	"/home/atelier", "/tmp", "/run", "/proc", "/dev",
}

// sandboxedCommand builds the child process for an exec request. By default the command
// runs as the non-root agent user (uid/gid 1001) inside bubblewrap with all capabilities
// dropped and fresh user/pid/ipc/uts namespaces (CRIT-01, and CRIT-03 for free).
//
// The real uid/gid are dropped to 1001 HERE, by runner (root, PID 1), *before* exec'ing
// bwrap — this is load-bearing, not belt-and-suspenders. bwrap is not setuid, so when it
// unshares a user namespace it can only map the sandbox uid onto its own real uid. If
// runner stayed root that real uid would be 0, giving the map `1001 0 1` — i.e.
// sandbox-uid-1001 == host-root: the agent would (DAC-wise) own every root file and could
// read /etc/shadow, the read-only mount being the only thing left stopping writes. Dropping
// to a genuine non-root host uid first makes the map `1001 1001 1`, so host-uid-0 files are
// foreign (they appear as nobody) and are correctly denied. (Empirically verified against a
// booted guest: without the drop `cat /etc/shadow` succeeds; with it, it is denied.)
//
// The network namespace is deliberately shared (no --unshare-net) so the egress path still
// works. Instead of bind-mounting the whole rootfs (the old `--bind / /`), the sandbox gets
// a CURATED read-only toolbox — /usr (+ usr-merge symlinks), /opt/atelier, and a small /etc
// allow-list — so the runner volume at /opt/runner (F-03) and sibling sessions (F-09) are
// simply never mounted. /dev and /proc are fresh, /tmp and /run are sized tmpfs, and the only
// writable real-storage paths are the agent's HOME and its own workspace (the per-session
// /sessions/<id> from p.SessionID, or the legacy /workspace). p.Privileged skips all of this
// and runs the command directly as root (operator/debug escape hatch). cwd and env are
// applied by the caller for both paths.
//
// A seccomp filter (F-13) is installed via `--seccomp <fd>`: the precompiled cBPF program is
// opened from the runner volume and handed to the bwrap child on fd 3 (Go maps ExtraFiles[0]
// there; nothing else uses ExtraFiles). bwrap creates its namespaces *before* applying the
// filter, so the profile can deny CLONE_NEWUSER (closing F-01) without breaking bwrap's own
// setup. This fails closed: a missing/unreadable blob is an error, never an unfiltered run.
// The caller owns the returned ExtraFiles fd and must close it after Start.
func sandboxedCommand(ctx context.Context, p execParams) (*exec.Cmd, error) {
	if p.Privileged {
		return exec.CommandContext(ctx, p.Cmd, p.Args...), nil
	}
	filter, err := os.Open(seccompFilterPath)
	if err != nil {
		return nil, fmt.Errorf("open seccomp filter %s: %w", seccompFilterPath, err)
	}
	args := []string{
		"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--uid", strconv.Itoa(agentUID), "--gid", strconv.Itoa(agentGID), "--cap-drop", "ALL",
		"--new-session", "--die-with-parent",
	}
	// Curated read-only toolbox in place of `--bind / /`: enough userland to run
	// Node+bash+python, but not the runner binary (F-03), host config, or other
	// sessions (F-09). Ubuntu is usr-merged, so /usr + symlinks cover /bin,/lib,…
	args = append(args,
		"--ro-bind", "/usr", "/usr",
		"--symlink", "usr/bin", "/bin",
		"--symlink", "usr/lib", "/lib",
		"--symlink", "usr/lib64", "/lib64",
		"--symlink", "usr/sbin", "/sbin",
		"--ro-bind", "/opt/atelier", "/opt/atelier",
		"--ro-bind", "/etc/ld.so.cache", "/etc/ld.so.cache",
		"--ro-bind", "/etc/ssl/certs", "/etc/ssl/certs",
	)
	for _, f := range sandboxOptionalEtcFiles {
		args = append(args, "--ro-bind-try", f, f)
	}
	// Fresh pseudo-fs + sized scratch; the only writable paths are HOME and the workspace.
	args = append(args,
		"--proc", "/proc",
		"--dev", "/dev",
		"--size", strconv.Itoa(tmpfsTmpBytes), "--tmpfs", "/tmp",
		"--size", strconv.Itoa(tmpfsRunBytes), "--tmpfs", "/run",
		"--bind", "/home/atelier", "/home/atelier",
	)
	rw := writablePaths(p)
	for _, w := range rw {
		// --bind-try, not --bind: a writable path may legitimately be absent for a given
		// exec (a stdin session with no share, a one-shot without a workspace). A hard bind
		// of a missing source aborts bwrap and the exec never starts.
		args = append(args, "--bind-try", w, w)
	}
	// Seccomp (cBPF on fd 3), then the Landlock shim, then the real command. The shim
	// self-applies a Landlock domain (FS allow-list + TCP-443 + IPC scope) and execve's
	// the command, so the agent runs behind both bwrap and Landlock. It lives under the
	// /opt/atelier ro-bind, so it is reachable inside the narrowed sandbox.
	args = append(args, "--seccomp", "3", "--", landlockShim)
	for _, w := range rw {
		args = append(args, "--rw", w)
	}
	args = append(args, "--", p.Cmd)
	args = append(args, p.Args...)
	cmd := exec.CommandContext(ctx, bwrapPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: agentUID, Gid: agentGID},
	}
	cmd.ExtraFiles = []*os.File{filter}
	return cmd, nil
}

// writablePaths returns the agent's writable storage (all bound tolerantly, since any of
// them may be absent for a given exec): the legacy /workspace share, the agent's actual
// workspace taken from its `--workspace <path>` argument (the Session Manager passes
// /sessions/<id> here — note this is decoupled from p.SessionID, which only names the stdin
// channel), and an ad-hoc cwd outside the read-only toolbox (e.g. `atelierctl exec -cwd
// /mnt/proj`). Binding only these — never the /sessions parent — keeps siblings invisible
// (F-09). The same list feeds the bwrap rw binds and the Landlock shim's --rw rules, so the
// two layers agree on exactly what is writable.
func writablePaths(p execParams) []string {
	var out []string
	seen := map[string]bool{}
	add := func(path string) {
		if path == "" || !filepath.IsAbs(path) {
			return
		}
		clean := filepath.Clean(path)
		if clean == "/" || seen[clean] {
			return
		}
		seen[clean] = true
		out = append(out, clean)
	}
	add(legacyWorkspace)
	add(workspaceArg(p.Args))
	if cwdNeedsBind(p.Cwd) {
		add(p.Cwd)
	}
	return out
}

// workspaceArg extracts the value of the agent's `--workspace` flag from its args,
// supporting both `--workspace <v>` and `--workspace=<v>` forms.
func workspaceArg(args []string) string {
	for i, a := range args {
		if a == "--workspace" && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--workspace="); ok {
			return v
		}
	}
	return ""
}

// cwdNeedsBind reports whether cwd is an absolute path outside the read-only toolbox
// and the already-mounted scratch paths, so it must be rw-bound to be usable.
func cwdNeedsBind(cwd string) bool {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return false
	}
	clean := filepath.Clean(cwd)
	if clean == "/" {
		return false
	}
	for _, p := range sandboxCoveredPrefixes {
		if clean == p || strings.HasPrefix(clean, p+"/") {
			return false
		}
	}
	return true
}
