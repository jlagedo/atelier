#!/usr/bin/env bash
#
# Build the utility-VM image bundle (design.md §7): a pinned set of
# kernel (vmlinuz) + boot initramfs (initrd) + Ubuntu rootfs (ext4 in a VHD),
# mirroring Cowork's claudevm.bundle. Sources live in image/{rootfs,initrd,kernel,guest};
# outputs go to image/bundle/ (gitignored).
#
# This is a scaffold: the rootfs stage is implemented (docker export -> ext4 via
# `mke2fs -d`, no root needed); kernel + initrd are documented stubs (need a chosen
# kernel + its modules — TODO M1). Run `image/build.sh check` to see tool readiness.
#
# Usage: image/build.sh {check|rootfs|initrd|kernel|bundle|all|clean}
set -euo pipefail

cd "$(dirname "$0")"

UBUNTU_VERSION="22.04"
ARCH="x86_64"
OUT="bundle"
WORK=".work"
ROOTFS_TAG="atelier-rootfs:${UBUNTU_VERSION}"

log() { printf '\033[1;34m[image]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[image] error:\033[0m %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

cmd_check() {
  log "tool readiness:"
  for t in docker mke2fs qemu-img sha256sum; do
    if have "$t"; then printf '  ok    %s\n' "$t"; else printf '  MISSING %s\n' "$t"; fi
  done
  log "rootfs needs: docker + mke2fs (+ qemu-img for VHD/VHDX). kernel/initrd: TODO(M1)."
}

cmd_rootfs() {
  have docker || die "docker not found (needed to export the Ubuntu rootfs)"
  have mke2fs || die "mke2fs not found (e2fsprogs; needed to build the ext4 image)"
  mkdir -p "$WORK/rootfs" "$OUT"

  log "building rootfs container image ($ROOTFS_TAG)"
  docker build -t "$ROOTFS_TAG" rootfs

  log "exporting container filesystem"
  cid="$(docker create "$ROOTFS_TAG")"
  trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' RETURN
  rm -rf "$WORK/rootfs"; mkdir -p "$WORK/rootfs"
  docker export "$cid" | tar -x -C "$WORK/rootfs"

  log "installing guest init"
  install -D -m 0755 guest/init.sh "$WORK/rootfs/sbin/init"

  # mke2fs -d populates an ext4 image from a directory WITHOUT mounting (no root).
  log "building ext4 image (base, read-only at runtime; overlay added per-session)"
  mke2fs -q -t ext4 -L atelier-root -d "$WORK/rootfs" -r 1 -N 0 -m 1 \
    "$WORK/rootfs.ext4" 2G

  if have qemu-img; then
    log "converting ext4 -> VHD (hcsshim PreferredRootFSType=vhd, design.md §7)"
    qemu-img convert -f raw -O vpc "$WORK/rootfs.ext4" "$OUT/rootfs.vhd"
  else
    log "qemu-img missing — leaving raw ext4 at $WORK/rootfs.ext4 (convert to VHD later)"
  fi
  log "rootfs done"
}

cmd_kernel() {
  log "fetching kernel"
  ./kernel/fetch-kernel.sh "$OUT"
}

cmd_initrd() {
  # TODO(M1): build a boot initramfs that loads image/initrd/modules.conf before
  # mounting root. Needs the chosen kernel version + its /lib/modules (mkinitramfs
  # / dracut). Output: $OUT/initrd.
  log "initrd: TODO(M1) — would mkinitramfs with modules from initrd/modules.conf"
}

cmd_bundle() {
  mkdir -p "$OUT"
  log "assembling bundle in $OUT/ (pin kernel+initrd+rootfs with sha256 .origin markers)"
  written=0
  for f in vmlinuz initrd rootfs.vhd rootfs.ext4; do
    if [ -f "$OUT/$f" ]; then
      sha256sum "$OUT/$f" | awk '{print $1}' > "$OUT/$f.origin"
      printf '  pinned %s -> %s.origin\n' "$f" "$f"
      written=1
    fi
  done
  [ "$written" = 1 ] || log "nothing to bundle yet (run rootfs/kernel/initrd first)"
  cat > "$OUT/manifest.txt" <<EOF
atelier vm bundle
ubuntu: ${UBUNTU_VERSION}
arch:   ${ARCH}
built:  $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
}

cmd_all() { cmd_kernel; cmd_rootfs; cmd_initrd; cmd_bundle; }

cmd_clean() { rm -rf "$WORK" "$OUT"/rootfs.* "$OUT"/vmlinuz* "$OUT"/initrd* "$OUT"/manifest.txt; log "cleaned"; }

case "${1:-}" in
  check)  cmd_check ;;
  rootfs) cmd_rootfs ;;
  initrd) cmd_initrd ;;
  kernel) cmd_kernel ;;
  bundle) cmd_bundle ;;
  all)    cmd_all ;;
  clean)  cmd_clean ;;
  *) echo "usage: $0 {check|rootfs|initrd|kernel|bundle|all|clean}" >&2; exit 2 ;;
esac
