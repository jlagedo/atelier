#!/usr/bin/env bash
# Obtain + pin the guest kernel (design.md §7). We do NOT hand-compile and we do
# NOT download separately: the matched kernel is installed by the rootfs Docker
# build (linux-image-generic-hwe-22.04), so its vmlinuz, /lib/modules/<ver>, and
# boot initramfs all come from one apt transaction — the coupling rule holds by
# construction. This script just extracts vmlinuz from that built tree and pins it.
#
# Usage: fetch-kernel.sh <OUT> <TREE>
#   OUT   output bundle dir (default: bundle)
#   TREE  exported rootfs tree to read /boot from (default: .work/rootfs)
#
# Output: $OUT/vmlinuz (+ vmlinuz.origin with a sha256, mirroring Cowork's bundle).
set -euo pipefail

OUT="${1:-bundle}"
TREE="${2:-.work/rootfs}"
mkdir -p "$OUT"

src="$(ls -1 "$TREE"/boot/vmlinuz-* 2>/dev/null | head -n1 || true)"
if [ -z "$src" ]; then
  echo "fetch-kernel: no $TREE/boot/vmlinuz-* found — run 'build.sh rootfs' first" >&2
  echo "             (the kernel is installed by image/rootfs/Dockerfile)" >&2
  exit 1
fi

cp "$src" "$OUT/vmlinuz"
sha256sum "$OUT/vmlinuz" | awk '{print $1}' > "$OUT/vmlinuz.origin"
echo "fetch-kernel: $(basename "$src") -> $OUT/vmlinuz (version ${src##*/vmlinuz-})" >&2
