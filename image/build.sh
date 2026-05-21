#!/usr/bin/env bash
#
# Build the utility-VM image bundle (design.md §7): a pinned set of
# kernel (vmlinuz) + boot initramfs (initrd) + Ubuntu rootfs (ext4 in a VHD),
# mirroring Cowork's claudevm.bundle. Sources live in image/{rootfs,initrd,kernel,guest};
# outputs go to image/bundle/ (gitignored).
#
# The matched kernel (linux-image-generic-hwe-22.04) + its /lib/modules + the boot
# initramfs are produced by the rootfs Docker build (one apt transaction, so the
# coupling rule of design.md §7 holds by construction). `rootfs` builds the ext4;
# `kernel`/`initrd` extract + pin vmlinuz/initrd from /boot of that same tree.
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

# stage_context assembles a SMALL, controlled Docker build context in $WORK/ctx so
# the rootfs Dockerfile can COPY the in-guest agent (Topology B, S5b.1). The repo
# root is too big to send as context (rootfs.vhd, .git, node_modules); instead we
# copy just the Dockerfile + packages/{agent,provider,protocol} SOURCE (no
# node_modules/dist) and build from there. npm install runs INSIDE the Docker build
# (linux/amd64) so the baked node_modules has the right platform binaries.
stage_pkg() {
  local p="$1"
  mkdir -p "$WORK/ctx/packages/$p"
  ( cd "../packages/$p" && tar --exclude=node_modules --exclude=dist --exclude=.git -cf - . ) \
    | ( cd "$WORK/ctx/packages/$p" && tar -xf - )
}

stage_context() {
  # protocol/src is generated (gitignored). It must exist before we stage it.
  [ -f "../packages/protocol/src/index.ts" ] \
    || die "packages/protocol/src is missing — run 'npm run protogen' at the repo root first"
  rm -rf "$WORK/ctx"
  mkdir -p "$WORK/ctx/packages"
  cp rootfs/Dockerfile "$WORK/ctx/Dockerfile"
  stage_pkg agent
  stage_pkg provider
  stage_pkg protocol
}

cmd_check() {
  log "tool readiness:"
  for t in docker go mke2fs qemu-img sha256sum; do
    if have "$t"; then printf '  ok    %s\n' "$t"; else printf '  MISSING %s\n' "$t"; fi
  done
  log "all stages need docker + go (guest daemon); rootfs also needs mke2fs (+ qemu-img for VHD/VHDX)."
}

# ensure_tree builds the rootfs container and exports its filesystem into
# $WORK/rootfs (with guest init installed), once per invocation. The exported tree
# carries the matched kernel: /boot/vmlinuz-<ver>, /boot/initrd.img-<ver>, and
# /lib/modules/<ver>. cmd_rootfs/cmd_kernel/cmd_initrd all build on it.
TREE_READY=0
ensure_tree() {
  [ "$TREE_READY" = 1 ] && return 0
  have docker || die "docker not found (needed to build/export the Ubuntu rootfs + kernel)"
  mkdir -p "$WORK/rootfs" "$OUT"

  log "staging build context (Dockerfile + packages/{agent,provider,protocol})"
  stage_context

  log "building rootfs container image ($ROOTFS_TAG)"
  docker build -t "$ROOTFS_TAG" "$WORK/ctx"

  log "exporting container filesystem"
  cid="$(docker create "$ROOTFS_TAG")"
  trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' RETURN
  rm -rf "$WORK/rootfs"; mkdir -p "$WORK/rootfs"
  docker export "$cid" | tar -x -C "$WORK/rootfs"

  log "installing guest init"
  install -D -m 0755 guest/init.sh "$WORK/rootfs/sbin/init"

  # Cross-compile the guest daemon (design.md §8 Hop 3) and inject it like init.
  # CGO_ENABLED=0 => a fully static binary (no glibc/loader coupling in the rootfs).
  have go || die "go not found (needed to cross-compile the guest daemon, services/cmd/guestd)"
  log "building guest daemon (guestd, linux/amd64 static)"
  local out; out="$(pwd)/$WORK/guestd"
  ( cd ../services && env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o "$out" ./cmd/guestd )
  install -D -m 0755 "$out" "$WORK/rootfs/usr/sbin/guestd"

  # Guest network forwarder (gvforwarder, design.md §8 Hop 3 channel 2 — S4.1):
  # the Network door's guest half. It is gvisor-tap-vsock's cmd/vm; build it at the
  # exact version services/go.mod pins (go install pkg@version resolves against the
  # library's own module, independent of our go.mod). guestd supervises it at boot.
  local gvbin gvver
  gvver="$(cd ../services && go list -m -f '{{.Version}}' github.com/containers/gvisor-tap-vsock)"
  log "building guest network forwarder (gvforwarder ${gvver}, linux/amd64 static)"
  gvbin="$(pwd)/$WORK/bin"
  ( env GOBIN="$gvbin" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
      go install -trimpath "github.com/containers/gvisor-tap-vsock/cmd/vm@${gvver}" )
  install -D -m 0755 "$gvbin/vm" "$WORK/rootfs/usr/sbin/gvforwarder"

  TREE_READY=1
}

# kver prints the matched kernel version (from /boot/vmlinuz-<ver>), or nothing.
# Glob (not ls|head) so an absent tree doesn't trip pipefail/set -e.
kver() {
  local g
  for g in "$WORK"/rootfs/boot/vmlinuz-*; do
    [ -e "$g" ] || continue
    echo "${g##*/vmlinuz-}"
    return 0
  done
}

cmd_rootfs() {
  have mke2fs || die "mke2fs not found (e2fsprogs; needed to build the ext4 image)"
  ensure_tree

  # mke2fs -d populates an ext4 image from a directory WITHOUT mounting (no root).
  # 4G leaves headroom for the matched /lib/modules(+extra) the kernel installs.
  log "building ext4 image (base, read-only at runtime; overlay added per-session)"
  mke2fs -q -t ext4 -L atelier-root -d "$WORK/rootfs" -r 1 -N 0 -m 1 \
    "$WORK/rootfs.ext4" 4G

  if have qemu-img; then
    log "converting ext4 -> VHD (hcsshim PreferredRootFSType=vhd, design.md §7)"
    qemu-img convert -f raw -O vpc "$WORK/rootfs.ext4" "$OUT/rootfs.vhd"
  else
    log "qemu-img missing — leaving raw ext4 at $WORK/rootfs.ext4 (convert to VHD later)"
  fi
  log "rootfs done"
}

cmd_kernel() {
  ensure_tree
  log "extracting + pinning kernel"
  ./kernel/fetch-kernel.sh "$OUT" "$WORK/rootfs"
}

cmd_initrd() {
  ensure_tree
  # Full boot initramfs: initramfs-tools' default MODULES=most, generated by the
  # kernel postinst inside the Docker build (image/initrd/modules.conf is reference
  # only under this policy). We just extract + pin it.
  log "extracting + pinning boot initramfs"
  src="$(ls -1 "$WORK/rootfs"/boot/initrd.img-* 2>/dev/null | head -n1 || true)"
  [ -n "$src" ] || die "no /boot/initrd.img-* in the rootfs tree (initramfs-tools/kernel not installed?)"
  cp "$src" "$OUT/initrd"
  sha256sum "$OUT/initrd" | awk '{print $1}' > "$OUT/initrd.origin"
  log "initrd: $(basename "$src") -> $OUT/initrd"
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
  k="$(kver)"
  cat > "$OUT/manifest.txt" <<EOF
atelier vm bundle
ubuntu: ${UBUNTU_VERSION}
arch:   ${ARCH}
kernel: ${k:-unknown}
built:  $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
}

# rootfs first builds the tree once; kernel/initrd extract from the retained
# $WORK/rootfs (ensure_tree is memoized for the invocation).
cmd_all() { cmd_rootfs; cmd_kernel; cmd_initrd; cmd_bundle; }

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
