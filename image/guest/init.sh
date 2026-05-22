#!/bin/sh
# Minimal guest init (PID 1) for the utility VM (design.md §7 boot sequence).
# Installed into the rootfs by image/build.sh. Mounts the kernel pseudo-filesystems
# and the host /workspace share, then hands off to the guest daemon.
set -eu

# Guarantee a usable PATH for PID 1 and everything it spawns (guestd's exec, plus
# the Network-door helpers it runs: modprobe, dhclient — S4.1).
export PATH=/usr/sbin:/usr/bin:/sbin:/bin

# The boot initramfs (S1.3) already mounts /proc, /sys, /dev and moves them into
# the real root on switch_root. Re-mounting them then fails with "already mounted"
# — which under `set -e` would exit PID 1 and panic the kernel. So tolerate it.
mount -t proc     proc     /proc  2>/dev/null || true
mount -t sysfs    sysfs    /sys   2>/dev/null || true
mount -t devtmpfs devtmpfs /dev   2>/dev/null || true
mount -t tmpfs    tmpfs    /tmp   2>/dev/null || true

# Read-only root (CRIT-05): the rootfs is mounted read-only, so the few paths that need
# runtime writes are tmpfs (ephemeral, per-boot; sized to bound RAM). /run and /var/tmp are
# general runtime scratch; /sessions is the parent for per-session 9p mount points (guestd
# mkdirs under it — so it MUST be writable on a ro root); /home/atelier is the non-root
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
mount -t tmpfs -o size=512m,mode=0700 tmpfs /home/atelier 2>/dev/null || true
chown 1001:1001 /home/atelier 2>/dev/null || true

# /workspace: the only persistent mount, shared from the host over 9p
# (design.md §8, §10 — Plan9/9p, not virtiofs). HCS serves the share over hvsock,
# so the mount needs a connected AF_VSOCK fd (trans=fd) — which a shell can't pass
# to mount(2). guestd does the mount itself (cmd/guestd/mount_linux.go, S3.1); we
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
# that the host control plane talks to. It binds hv_sock, so make sure the
# transport is loaded (tolerant: it may be built in, or already auto-loaded).
modprobe hv_sock 2>/dev/null || true

# guestd becomes the long-running PID 1. Fall back to a shell if it isn't shipped
# (e.g. a bundle built without it) so the VM still boots and stays debuggable.
if [ -x /usr/sbin/guestd ]; then
  echo "atelier guest init: starting guestd (vsock RPC server) ..."
  exec /usr/sbin/guestd
fi
echo "atelier guest init: guestd not installed — dropping to shell"
exec /bin/sh
