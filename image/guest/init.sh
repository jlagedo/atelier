#!/bin/sh
# Minimal guest init (PID 1) for the utility VM (design.md §7 boot sequence).
# Installed into the rootfs by image/build.sh. Mounts the kernel pseudo-filesystems
# and the host /workspace share, then hands off to the guest daemon.
set -eu

# Guarantee a usable PATH for PID 1 and everything it spawns (runner's exec, plus
# the Network-door helpers it runs: modprobe, dhclient — S4.1).
export PATH=/usr/sbin:/usr/bin:/sbin:/bin

# The boot initramfs (S1.3) already mounts /proc, /sys, /dev and moves them into
# the real root on switch_root. Re-mounting them then fails with "already mounted"
# — which under `set -e` would exit PID 1 and panic the kernel. So tolerate it.
mount -t proc     proc     /proc  2>/dev/null || true
mount -t sysfs    sysfs    /sys   2>/dev/null || true
mount -t devtmpfs devtmpfs /dev   2>/dev/null || true
mount -t tmpfs    tmpfs    /tmp   2>/dev/null || true

# cgroup v2 (unified): mount it and delegate the controllers runner needs so it can place
# each sandboxed exec in its own cgroup with pids.max/memory.max/cpu.max (F-06, runaway
# containment). Tolerant: if cgroup2 is unavailable the per-exec limiter soft-fails (runner
# logs a warning and runs without limits) rather than wedging boot.
mount -t cgroup2 none /sys/fs/cgroup 2>/dev/null || true
echo "+pids +memory +cpu" > /sys/fs/cgroup/cgroup.subtree_control 2>/dev/null || true

# Read-only root (CRIT-05): the rootfs is mounted read-only, so the few paths that need
# runtime writes are tmpfs (ephemeral, per-boot; sized to bound RAM). /run and /var/tmp are
# general runtime scratch; /sessions is the parent for per-session mount points (under
# Hyper-V runner mkdirs a 9p mount per session here; under VZ runner mounts the single
# virtio-fs device once at /sessions and each session is a <tag> subdir, S7 — either way it
# MUST be writable on a ro root); /mnt is shadowed so runner can mkdir arbitrary targets like
# /mnt/proj for non-/sessions workspace shares; /home/atelier is the non-root
# agent's writable HOME (CRIT-01), chowned to that uid after the tmpfs is mounted. Tolerate
# "already mounted" (initramfs may have done some) so a re-mount never wedges PID 1.
# Explicit mode= on every tmpfs: tmpfs defaults its root dir to 1777 (world-writable,
# like /tmp), which would re-introduce world-writable system paths — the very thing
# CRIT-05 set out to remove — just on tmpfs instead of the ext4. /run and /sessions are
# root-owned runtime dirs (0755); /var/tmp keeps the conventional 1777 sticky scratch;
# /home/atelier is the agent's private HOME (0700) chowned to its uid after mounting.
mount -t tmpfs -o size=64m,mode=0755  tmpfs /run          2>/dev/null || true
mount -t tmpfs -o size=64m,mode=1777  tmpfs /var/tmp      2>/dev/null || true
mount -t tmpfs -o size=16m,mode=0755  tmpfs /sessions     2>/dev/null || true
mount -t tmpfs -o size=16m,mode=0755  tmpfs /mnt          2>/dev/null || true
mount -t tmpfs -o size=512m,mode=0700 tmpfs /home/atelier 2>/dev/null || true
chown 1001:1001 /home/atelier 2>/dev/null || true

# /workspace: the only persistent mount, shared from the host over 9p
# (design.md §8, §10 — Plan9/9p, not virtiofs). HCS serves the share over hvsock,
# so the mount needs a connected AF_VSOCK fd (trans=fd) — which a shell can't pass
# to mount(2). runner does the mount itself (cmd/runner/mount_linux.go, S3.1); we
# just ensure the mount point exists.
mkdir -p /workspace

# Boot diagnostics (design.md §7 coupling rule, S1.3 verify): the running kernel
# version must match a /lib/modules/<ver> dir, and modprobe must work — proof that
# the matched kernel + initrd + rootfs are coupled. Guarded so a miss never wedges init.
echo "atelier guest init: kernel $(uname -r) | /lib/modules: $(ls /lib/modules 2>/dev/null | tr '\n' ' ')"
if modprobe loop 2>/dev/null; then
  echo "atelier guest init: modprobe ok (module ecosystem matched)"
else
  echo "atelier guest init: modprobe failed (kernel/modules mismatch?)"
fi

# Hand off to the guest daemon (design.md §8 Hop 3): the AF_VSOCK JSON-RPC server
# the host control plane talks to. AF_VSOCK is backed by a hypervisor-specific
# transport — hv_sock under Hyper-V (Windows), virtio-vsock under Apple's
# Virtualization.framework (macOS). Load both tolerantly: the one matching this
# host registers /dev/vsock, the other no-ops (it may also be built in or already
# auto-loaded). vmw_vsock_virtio_transport pulls in the vsock core + _common, which
# is what creates /dev/vsock for runner to bind (S5).
modprobe hv_sock 2>/dev/null || true
modprobe vmw_vsock_virtio_transport 2>/dev/null || true

# Files door: load the virtio-fs client so runner can `mount -t virtiofs <tag> <target>`
# the host shares (S6, macOS/Virtualization.framework). Tolerant like the vsock loads:
# it no-ops where virtiofs is built-in or absent (e.g. the Hyper-V bundle, which mounts 9p).
modprobe virtiofs 2>/dev/null || true

# Files door over 9p (HCS/Hyper-V serves shares as Plan9 over hvsock, trans=fd). Preload the
# 9p stack so runner's runtime `mount -t 9p` works WITHOUT the kernel module autoloader —
# which the modules_disabled latch below would otherwise block. Tolerant: no-ops on macOS/VZ
# (the virtiofs path) or where built-in.
modprobe 9pnet_fd 2>/dev/null || true
modprobe 9pnet_virtio 2>/dev/null || true
modprobe 9p 2>/dev/null || true

# Egress prep: runner does `modprobe tun` at runtime (cmd/runner/egress_linux.go) for the
# tap0 interface. Preload it HERE so the modules_disabled latch below does not block it.
modprobe tun 2>/dev/null || true

# Kernel hardening sysctls (write /proc/sys directly — no systemd/sysctl in the guest). Each
# guarded with `|| true` so a knob absent on this kernel never wedges PID 1.
#   io_uring_disabled=2  belt-and-suspenders behind the seccomp deny; also covers the
#                        privileged escape hatch, which runs with no seccomp filter.
#   kptr_restrict=2      hide kernel pointers from all users (defeat KASLR leaks — F-04).
#   yama/ptrace_scope=2  restrict ptrace to CAP_SYS_PTRACE only.
echo 2 > /proc/sys/kernel/io_uring_disabled 2>/dev/null || true
echo 2 > /proc/sys/kernel/kptr_restrict     2>/dev/null || true
echo 2 > /proc/sys/kernel/yama/ptrace_scope 2>/dev/null || true

# One-way latch (F-16): forbid further module loads for the VM's lifetime. MUST be the LAST
# module-related action — every module the guest needs (loop, vsock, virtiofs, the 9p stack,
# tun; ext4 via initramfs) is already loaded above.
echo 1 > /proc/sys/kernel/modules_disabled 2>/dev/null || true

# runner becomes the long-running PID 1 (the vsock RPC server). Neither runner NOR the
# in-guest agent is baked into the rootfs — they ship together on ONE read-only ext4 volume
# (image/build.sh runner; LABEL=runner) attached as a second disk, so both rebuild in seconds
# without rebuilding the whole rootfs. Mount it by label so it's device-order-independent and
# needs no udev (libblkid scans /dev directly), at /opt: runner lives at /opt/runner/atelier-runner and
# the agent at /opt/atelier (the paths the Session Manager execs). The volume is the sole
# delivery path on every target; a missing/unmountable volume drops to a shell so the failure
# is visible on the serial console. /opt exists in the rootfs (read-only) and the mount shadows
# it — a runtime `mkdir` there would EROFS-fail (and, under `set -e`, kill PID 1), so we don't.
if mount -t ext4 -o ro -L runner /opt 2>/dev/null && [ -x /opt/runner/atelier-runner ]; then
  echo "atelier guest init: starting runner from volume (LABEL=runner, /opt) ..."
  exec /opt/runner/atelier-runner
fi
echo "atelier guest init: guest volume (LABEL=runner) not mounted — dropping to shell"
exec /bin/sh
