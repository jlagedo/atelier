#!/usr/bin/env bash
# Obtain and pin the guest kernel (design.md §7). We do NOT hand-compile — we
# borrow a prebuilt kernel that has the Hyper-V/VMBus + virtio + 9p drivers.
#
# The kernel must match the rootfs's /lib/modules/<ver> (the coupling rule), so
# whichever kernel is chosen here, its modules must be installed into the rootfs.
#
# Output: $OUT/vmlinuz (+ vmlinuz.origin with a sha256, mirroring Cowork's bundle).
set -euo pipefail

OUT="${1:-bundle}"
mkdir -p "$OUT"

# TODO(M1): pick + fetch a kernel. Candidates (design.md §7, §16):
#   A) the LCOW kernel that ships with hcsshim tooling  — known to boot an HCS UVM
#      (M0 throwaway bootstrap only; do not marry it to our Ubuntu userland).
#   B) a generic Ubuntu kernel matched to the Ubuntu 22.04 rootfs (the M1 target);
#      install its /lib/modules/<ver> into the rootfs and build a matching initrd.
echo "fetch-kernel: TODO(M1) — no kernel source wired yet" >&2
echo "             would write ${OUT}/vmlinuz and ${OUT}/vmlinuz.origin (sha256)" >&2
exit 0
