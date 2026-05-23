#!/usr/bin/env bash
# Obtain + pin the guest kernel (design.md §7). We do NOT hand-compile and we do
# NOT download separately: the matched kernel is installed by the rootfs Docker
# build (linux-image-generic-hwe-22.04), so its vmlinuz, /lib/modules/<ver>, and
# boot initramfs all come from one apt transaction — the coupling rule holds by
# construction. This script just extracts vmlinuz from that built tree and pins it.
#
# Usage: fetch-kernel.sh <OUT> <TREE> [DISK]
#   OUT   output bundle dir (default: bundle)
#   TREE  exported rootfs tree to read /boot from (default: .work/rootfs)
#   DISK  target disk format (vhd|raw); raw selects the VZ kernel format (see below)
#
# Output: $OUT/vmlinuz (+ vmlinuz.origin with a sha256, mirroring Cowork's bundle).
#
# Kernel format by target: Ubuntu's arm64 /boot/vmlinuz-* is a gzip-compressed
# Image. Windows/HCS (vhd) boots that compressed vmlinuz directly, but Apple's
# VZLinuxBootLoader (raw / darwin-arm64-vz) cannot boot a compressed kernel — it
# needs the decompressed arm64 `Image`. So for raw targets we gunzip into vmlinuz
# (same filename, decompressed content); for vhd we copy the kernel as-is. The
# x86 bzImage is not a gzip container at byte 0, so this never touches it.
set -euo pipefail

OUT="${1:-bundle}"
TREE="${2:-.work/rootfs}"
DISK="${3:-vhd}"
mkdir -p "$OUT"

src="$(ls -1 "$TREE"/boot/vmlinuz-* 2>/dev/null | head -n1 || true)"
if [ -z "$src" ]; then
  echo "fetch-kernel: no $TREE/boot/vmlinuz-* found — run 'build.sh rootfs' first" >&2
  echo "             (the kernel is installed by image/rootfs/Dockerfile)" >&2
  exit 1
fi

if [ "$DISK" = raw ] && gzip -t "$src" 2>/dev/null; then
  gunzip -c "$src" > "$OUT/vmlinuz"
  echo "fetch-kernel: $(basename "$src") -> $OUT/vmlinuz (gunzipped to arm64 Image for VZ)" >&2
else
  cp "$src" "$OUT/vmlinuz"
  echo "fetch-kernel: $(basename "$src") -> $OUT/vmlinuz (version ${src##*/vmlinuz-})" >&2
fi
sha256sum "$OUT/vmlinuz" | awk '{print $1}' > "$OUT/vmlinuz.origin"
