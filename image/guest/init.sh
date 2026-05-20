#!/bin/sh
# Minimal guest init (PID 1) for the utility VM (design.md §7 boot sequence).
# Installed into the rootfs by image/build.sh. Mounts the kernel pseudo-filesystems
# and the host /workspace share, then hands off to the guest daemon.
set -eu

mount -t proc     proc     /proc
mount -t sysfs    sysfs    /sys
mount -t devtmpfs devtmpfs /dev   2>/dev/null || true
mount -t tmpfs    tmpfs    /tmp

# /workspace: the only persistent mount, shared from the host over 9p
# (design.md §8 — Plan9/9p, not virtiofs). The mount tag is set by the host's
# compute-system doc; "workspace" is a placeholder.
mkdir -p /workspace
mount -t 9p -o trans=virtio,version=9p2000.L workspace /workspace 2>/dev/null \
  || echo "init: /workspace 9p mount not available (host share not configured yet)"

# TODO(M5b): exec the guest daemon (services/cmd/guestd) as the long-running PID 1.
echo "atelier guest init: scaffold — guest daemon not installed yet (M5b)"
exec /bin/sh
