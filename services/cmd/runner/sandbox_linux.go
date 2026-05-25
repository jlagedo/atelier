//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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
// works. The whole root filesystem is bind-mounted in but stays read-only (kernel-enforced
// — CRIT-05), while /dev, /proc and /tmp are fresh; the 9p /workspace and /sessions shares
// ride in via the recursive bind. p.Privileged skips all of this and runs the command
// directly as root (operator/debug escape hatch). cwd and env are applied by the caller for
// both paths.
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
		"--bind", "/", "/",
		"--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp",
		"--seccomp", "3",
		"--", p.Cmd,
	}
	args = append(args, p.Args...)
	cmd := exec.CommandContext(ctx, bwrapPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: agentUID, Gid: agentGID},
	}
	cmd.ExtraFiles = []*os.File{filter}
	return cmd, nil
}
