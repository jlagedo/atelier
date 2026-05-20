#!/bin/sh
# Minimal guest init (PID 1) for the utility VM (design.md §7 boot sequence).
# Installed into the rootfs by image/build.sh. Mounts the kernel pseudo-filesystems
# and the host /workspace share, then hands off to the guest daemon.
set -eu

# The boot initramfs (S1.3) already mounts /proc, /sys, /dev and moves them into
# the real root on switch_root. Re-mounting them then fails with "already mounted"
# — which under `set -e` would exit PID 1 and panic the kernel. So tolerate it.
mount -t proc     proc     /proc  2>/dev/null || true
mount -t sysfs    sysfs    /sys   2>/dev/null || true
mount -t devtmpfs devtmpfs /dev   2>/dev/null || true
mount -t tmpfs    tmpfs    /tmp   2>/dev/null || true

# /workspace: the only persistent mount, shared from the host over 9p
# (design.md §8 — Plan9/9p, not virtiofs). The mount tag is set by the host's
# compute-system doc; "workspace" is a placeholder.
mkdir -p /workspace
mount -t 9p -o trans=virtio,version=9p2000.L workspace /workspace 2>/dev/null \
  || echo "init: /workspace 9p mount not available (host share not configured yet)"

# Boot diagnostics (design.md §7 coupling rule, S1.3 verify): the running kernel
# version must match a /lib/modules/<ver> dir, and modprobe must work — proof that
# the matched kernel + initrd + rootfs are coupled. Guarded so a miss never wedges init.
echo "atelier guest init: kernel $(uname -r) | /lib/modules: $(ls /lib/modules 2>/dev/null | tr '\n' ' ')"
if modprobe loop 2>/dev/null; then
  echo "atelier guest init: modprobe ok (module ecosystem matched)"
else
  echo "atelier guest init: modprobe failed (kernel/modules mismatch?)"
fi

# TODO(M5b): exec the guest daemon (services/cmd/guestd) as the long-running PID 1.
echo "atelier guest init: scaffold — guest daemon not installed yet (M5b)"
exec /bin/sh
