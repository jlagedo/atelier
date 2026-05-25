//go:build linux

// Command atelier-landlock is the innermost exec shim for the in-guest agent sandbox.
// bwrap execs it as the sandbox target; it applies a Landlock LSM ruleset to itself and
// then execve's the real command, so the agent inherits the Landlock domain. Running here
// — after bwrap's pivot_root + uid drop and the seccomp install, before Node spawns its
// thread pool — means the path rules resolve against the sandbox view and the whole agent
// process tree is confined.
//
// Usage: atelier-landlock [--rw <path>]... -- <cmd> [args...]
//
// FS rules mirror the bwrap bind allow-list (ro toolbox, rw scratch + workspace); the
// network is restricted to outbound TCP 443 (DNS is UDP, so it is unaffected); and IPC
// scope (abstract UNIX sockets + signals) is confined to this Landlock domain. Everything
// is BestEffort: on a kernel missing a Landlock ABI level the matching restriction is
// skipped rather than failing, since bwrap + seccomp + dropped caps still bound the agent.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/landlock-lsm/go-landlock/landlock"
)

func main() {
	rw, rest := parseArgs(os.Args[1:])
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "atelier-landlock: usage: atelier-landlock [--rw <path>]... -- <cmd> [args...]")
		os.Exit(2)
	}

	// Resolve the command before locking down, so PATH lookup isn't itself constrained.
	path := rest[0]
	if resolved, err := exec.LookPath(rest[0]); err == nil {
		path = resolved
	}

	if err := applyLandlock(rw); err != nil {
		// Soft-fail: the agent still runs behind bwrap + seccomp + cap-drop.
		fmt.Fprintf(os.Stderr, "atelier-landlock: ruleset not fully applied: %v\n", err)
	} else {
		fmt.Fprintln(os.Stderr, "atelier-landlock: Landlock ruleset applied (BestEffort V8)")
	}

	if err := syscall.Exec(path, rest, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "atelier-landlock: exec %s: %v\n", rest[0], err)
		os.Exit(127)
	}
}

// parseArgs pulls leading `--rw <path>` pairs off the argv, stops at the `--` separator,
// and returns the remaining command vector.
func parseArgs(a []string) (rw []string, rest []string) {
	i := 0
	for i < len(a) {
		if a[i] == "--" {
			i++
			break
		}
		if a[i] == "--rw" && i+1 < len(a) {
			if a[i+1] != "" {
				rw = append(rw, a[i+1])
			}
			i += 2
			continue
		}
		break // unrecognized token before `--`: treat the rest as the command
	}
	return rw, a[i:]
}

// applyLandlock self-restricts the process (and its execve'd children) to the agent's
// allow-list. The rule set mirrors the bwrap binds; /dev keeps WithIoctlDev so terminal
// ioctls (isatty) don't break Node, and /proc stays readable+writable for runtime introspection
// (it is already a fresh, bwrap-masked instance).
func applyLandlock(rw []string) error {
	// Every FS rule is IgnoreIfMissing: a single absent path must not make Restrict drop
	// the WHOLE domain (which would silently disable Landlock) — the rest still enforces.
	rules := []landlock.Rule{
		landlock.RODirs("/usr", "/opt/atelier").IgnoreIfMissing(),
		landlock.RWDirs("/home/atelier", "/tmp", "/run").IgnoreIfMissing(),
		landlock.RWDirs("/dev").WithIoctlDev().IgnoreIfMissing(),
		landlock.RWDirs("/proc").IgnoreIfMissing(),
		landlock.ROFiles("/etc/ld.so.cache").IgnoreIfMissing(),
		landlock.RODirs("/etc/ssl/certs").IgnoreIfMissing(),
		landlock.ROFiles(
			"/etc/resolv.conf", "/etc/nsswitch.conf", "/etc/passwd", "/etc/group",
			"/etc/localtime", "/etc/hosts", "/etc/host.conf", "/etc/gai.conf",
		).IgnoreIfMissing(),
		landlock.ConnectTCP(443),
	}
	for _, p := range rw {
		rules = append(rules, landlock.RWDirs(p).IgnoreIfMissing())
	}
	// V8 BestEffort applies FS + net + IPC-scope restrictions, downgrading to the highest
	// ABI the running kernel supports (or to a no-op if Landlock is absent).
	return landlock.V8.BestEffort().Restrict(rules...)
}
