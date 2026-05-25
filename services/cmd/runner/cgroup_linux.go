//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

// cgroupRoot is the unified (v2) hierarchy mounted by image/guest/init.sh.
const cgroupRoot = "/sys/fs/cgroup"

// cgroupLimits are the per-exec resource caps (F-06): contain fork bombs, runaway
// memory, and CPU spin from hostile code inside the sandbox.
var cgroupLimits = []struct{ file, val string }{
	{"pids.max", "512"},
	{"memory.max", "2147483648"}, // 2 GiB
	{"memory.swap.max", "0"},     // no swap escape hatch
	{"cpu.max", "200000 100000"}, // 2 cores (200ms quota / 100ms period)
}

var cgroupSeq atomic.Uint64

// applyCgroupLimits places the (non-privileged) sandboxed child in a fresh cgroup v2
// with pids/memory/cpu caps, using SysProcAttr.CgroupFD so the child is created directly
// inside the cgroup (no post-Start race). It SOFT-FAILS: if cgroup2 is unavailable, a dir
// can't be created, or a controller isn't delegated, it logs a warning and runs the child
// without limits — the bwrap+seccomp boundary still holds. Privileged execs (nil
// SysProcAttr, the debug escape hatch) are skipped. Returns a cleanup to run after Wait.
func applyCgroupLimits(log *slog.Logger, cmd *exec.Cmd, _ execParams) func() {
	noop := func() {}
	if cmd.SysProcAttr == nil {
		return noop // privileged bypass — leave it unconstrained by design
	}
	if _, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers")); err != nil {
		log.Warn("cgroup v2 unavailable — running exec without resource limits", "err", err)
		return noop
	}
	dir := filepath.Join(cgroupRoot, fmt.Sprintf("atelier-exec-%d-%d", os.Getpid(), cgroupSeq.Add(1)))
	if err := os.Mkdir(dir, 0o755); err != nil {
		log.Warn("cgroup create failed — running exec without resource limits", "dir", dir, "err", err)
		return noop
	}
	for _, l := range cgroupLimits {
		if err := os.WriteFile(filepath.Join(dir, l.file), []byte(l.val), 0o644); err != nil {
			// A single undelegated controller shouldn't drop all limits; keep going.
			log.Warn("cgroup limit not applied", "file", l.file, "val", l.val, "err", err)
		}
	}
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		log.Warn("cgroup open failed — running exec without resource limits", "dir", dir, "err", err)
		_ = os.Remove(dir)
		return noop
	}
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = fd
	return func() {
		_ = syscall.Close(fd)
		// rmdir only succeeds once the cgroup is empty (child reaped). Wait() has
		// already returned by the time this runs, but the kernel may lag a moment.
		for i := 0; i < 50; i++ {
			if err := syscall.Rmdir(dir); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
