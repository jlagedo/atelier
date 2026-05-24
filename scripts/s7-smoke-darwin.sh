#!/usr/bin/env bash
# S7 runtime-share smoke test (macos-port-execution.md §S7) — Apple Silicon only.
#
# Two layers, run in sequence:
#
#   BLACK-BOX (broker + vmctl): boots a VM the way the product does and exercises the
#   real attach/detach RPCs. It (1) reproduces the S6 single-workspace mount, then
#   (2) drives the multi-session path (-tag/-target) — the shipped S7 base/<tag> shape:
#   the single virtio-fs device is mounted once at /sessions and each session is a named
#   subdir, so two sessions live concurrently as /sessions/<tag>, and detaching one leaves
#   the other intact. (Before S7 this rolled back, because guestd mounted the per-session
#   tag instead of the device tag; that is the gap this PR closes.)
#
#   WHITE-BOX (gated Go probe): compiles services/internal/vmm's tests, codesigns the
#   test binary with the virtualization entitlement, and runs TestS7RuntimeShareProbe.
#   That probe drives the driver directly (host-only SetShare) to pin the shipped shape:
#   the lone "workspace" tag mounts at /workspace (S6); a session set mounts the device
#   once at the base and appears as /sessions/<tag>, with live add + sibling-safe detach
#   visible without a remount.
#
# Usage:
#   scripts/s7-smoke-darwin.sh                 # build everything, then black-box, then probe
#   ONLY=blackbox scripts/s7-smoke-darwin.sh   # just the vmctl smoke
#   ONLY=whitebox scripts/s7-smoke-darwin.sh   # just the gated Go probe
#   SKIP_BUILD=1 scripts/s7-smoke-darwin.sh    # reuse existing signed services/bin/host + bundle
#   BUILD_BUNDLE=1 scripts/s7-smoke-darwin.sh  # force a guest-bundle rebuild (needed after guest changes)
#
# The guest bundle (image/bundle/darwin-arm64-vz) is built automatically when missing
# via image/build.sh (Docker cross-arch build). It is NOT rebuilt when already present
# unless BUILD_BUNDLE=1 — so after touching guest code (init.sh, guestd) pass that flag.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEFAULT_BUNDLE="$REPO/image/bundle/darwin-arm64-vz"
BUNDLE="${ATELIER_BUNDLE_DIR:-$DEFAULT_BUNDLE}"
ENTITLEMENTS="$REPO/services/packaging/darwin/atelier-vm.entitlements"
ADDR="/tmp/atelier-s7-host.sock"
ONLY="${ONLY:-all}"

pass=(); fail=(); note=()
ok()   { echo "  ✅ $*"; pass+=("$*"); }
bad()  { echo "  ❌ $*"; fail+=("$*"); }
info() { echo "  • $*"; note+=("$*"); }
hdr()  { echo; echo "=== $* ==="; }

bundle_complete() {
  for f in vmlinuz initrd rootfs.raw; do [[ -f "$BUNDLE/$f" ]] || return 1; done
}

ensure_bundle() {
  if [[ "${BUILD_BUNDLE:-}" == "1" ]] || ! bundle_complete; then
    if [[ "$BUNDLE" != "$DEFAULT_BUNDLE" ]]; then
      echo "ATELIER_BUNDLE_DIR=$BUNDLE is incomplete and not the default build output;"
      echo "build it yourself or unset ATELIER_BUNDLE_DIR to let this script build it."
      exit 1
    fi
    hdr "build guest bundle (darwin-arm64-vz)"
    ( cd "$REPO/image" && TARGET=darwin-arm64-vz ./build.sh all )
  fi
  bundle_complete || { echo "bundle still incomplete at $BUNDLE after build"; exit 1; }
}

# ---- build + sign --------------------------------------------------------------------
if [[ "${SKIP_BUILD:-}" != "1" ]]; then
  hdr "build + sign (broker, vmctl)"
  "$REPO/scripts/build-sign-darwin.sh"
fi
ensure_bundle

HOST="$REPO/services/bin/host"
VMCTL="$REPO/services/bin/vmctl"
# vmctl wants the subcommand FIRST (a leading "-" makes it fall back to getStatus),
# so inject -addr right after the subcommand, not before it.
vm() { local sub="$1"; shift; "$VMCTL" "$sub" -addr "$ADDR" "$@"; }

# =====================================================================================
# BLACK-BOX: broker + vmctl
# =====================================================================================
blackbox() {
  hdr "BLACK-BOX: boot via broker + vmctl"
  rm -f "$ADDR"
  "$HOST" -addr "$ADDR" 2>/tmp/atelier-s7-broker.log &
  local broker=$!
  disown "$broker" 2>/dev/null || true  # suppress the shell's "Terminated" notice when the trap kills it
  local work; work="$(mktemp -d)"
  # One RETURN trap for the whole function (a second `trap ... RETURN` would replace
  # this one, leaking the broker): stop the VM, kill the broker, drop the temp dir.
  trap 'vm stopVM -id vm0 >/dev/null 2>&1 || true; kill "$broker" >/dev/null 2>&1 || true; rm -rf "$work"' RETURN

  # Wait for the broker to listen.
  for _ in $(seq 1 50); do vm getStatus >/dev/null 2>&1 && break; sleep 0.2; done
  vm getStatus >/dev/null 2>&1 || { bad "broker did not come up (see /tmp/atelier-s7-broker.log)"; return; }

  mkdir -p "$work/A" "$work/s1" "$work/s2"
  echo alpha   >"$work/A/a.txt"
  echo bravo   >"$work/s1/b.txt"
  echo charlie >"$work/s2/c.txt"

  if ! vm createVM -id vm0 -kernel "$BUNDLE/vmlinuz" -initrd "$BUNDLE/initrd" -rootfs "$BUNDLE/rootfs.raw" >/dev/null 2>&1; then
    bad "createVM failed"; tail -8 /tmp/atelier-s7-broker.log; return
  fi
  if ! vm startVM -id vm0 >/dev/null 2>&1; then
    bad "startVM failed (boot)"; tail -20 /tmp/atelier-s7-broker.log; return
  fi
  ok "VM booted"

  # --- Phase A: single workspace (reproduce S6) ---
  echo; echo "[A] single workspace (legacy attach)"
  if vm attachWorkspace -id vm0 -path "$work/A" >/dev/null 2>&1; then
    local ls; ls="$(vm exec -id vm0 -- ls -A /workspace 2>&1 || true)"
    if grep -q a.txt <<<"$ls"; then ok "single share: a.txt visible at /workspace"; else bad "single share: a.txt NOT at /workspace ($ls)"; fi
    vm detachWorkspace -id vm0 >/dev/null 2>&1 && ok "single share: detached cleanly" || bad "single share: detach failed"
  else
    bad "single share: attachWorkspace failed"
  fi

  # --- Phase B: multi-session via the product RPCs (the shipped S7 base/<tag> shape) ---
  echo; echo "[B] multi-session (-tag/-target): two sessions on one VM"
  vm attachWorkspace -id vm0 -path "$work/s1" -tag s1 -target /sessions/s1 >/dev/null 2>&1 \
    && ok "multi s1: attached" || { bad "multi s1: attach failed"; tail -8 /tmp/atelier-s7-broker.log; }
  vm attachWorkspace -id vm0 -path "$work/s2" -tag s2 -target /sessions/s2 >/dev/null 2>&1 \
    && ok "multi s2: attached (2nd live session on one VM)" || { bad "multi s2: attach failed"; tail -8 /tmp/atelier-s7-broker.log; }

  # Both sessions visible concurrently, each at its own /sessions/<tag> subdir.
  local ls1 ls2
  ls1="$(vm exec -id vm0 -- ls -A /sessions/s1 2>&1 || true)"
  ls2="$(vm exec -id vm0 -- ls -A /sessions/s2 2>&1 || true)"
  grep -q b.txt <<<"$ls1" && ok "multi s1: b.txt at /sessions/s1" || bad "multi s1: b.txt NOT at /sessions/s1 ($ls1)"
  grep -q c.txt <<<"$ls2" && ok "multi s2: c.txt at /sessions/s2" || bad "multi s2: c.txt NOT at /sessions/s2 ($ls2)"

  # Sibling-safe detach: drop s1; s2 must survive (the base mount stays for live sessions).
  vm detachWorkspace -id vm0 -tag s1 >/dev/null 2>&1 && ok "multi s1: detached" || bad "multi s1: detach failed"
  local after1 after2
  after1="$(vm exec -id vm0 -- ls -A /sessions/s1 2>&1 || true)"
  after2="$(vm exec -id vm0 -- ls -A /sessions/s2 2>&1 || true)"
  grep -q b.txt <<<"$after1" && bad "multi: /sessions/s1 should be empty after detach ($after1)" || ok "multi: /sessions/s1 gone after detach"
  grep -q c.txt <<<"$after2" && ok "multi: sibling /sessions/s2 survived s1 detach" || bad "multi: sibling /sessions/s2 lost after s1 detach ($after2)"
  vm detachWorkspace -id vm0 -tag s2 >/dev/null 2>&1 && ok "multi s2: detached" || bad "multi s2: detach failed"

  vm stopVM -id vm0 >/dev/null 2>&1 || true
  ok "stopVM clean"
}

# =====================================================================================
# WHITE-BOX: gated Go probe (driver-direct host-only SetShare)
# =====================================================================================
whitebox() {
  hdr "WHITE-BOX: gated Go probe (driver SetShare + guest probe)"
  local bin="/tmp/atelier-s7-probe.test"
  ( cd "$REPO/services" && CGO_ENABLED=1 go test -c -o "$bin" ./internal/vmm )
  codesign --force --sign - --options runtime --entitlements "$ENTITLEMENTS" "$bin"
  echo "signed probe binary: $bin"
  echo
  ATELIER_VZ_SMOKE=1 ATELIER_BUNDLE_DIR="$BUNDLE" \
    "$bin" -test.run TestS7RuntimeShareProbe -test.v && ok "probe completed (read the VERDICT INPUTS block above)" \
                                                      || bad "probe failed (see log above)"
}

case "$ONLY" in
  blackbox) blackbox || true ;;
  whitebox) whitebox || true ;;
  all)      blackbox || true; whitebox || true ;;
  *) echo "ONLY must be all|blackbox|whitebox"; exit 1 ;;
esac

# ---- summary -------------------------------------------------------------------------
hdr "S7 SMOKE SUMMARY"
printf '%s\n' "${pass[@]/#/  ✅ }" 2>/dev/null || true
printf '%s\n' "${note[@]/#/  • }"  2>/dev/null || true
if ((${#fail[@]})); then
  printf '%s\n' "${fail[@]/#/  ❌ }"
  echo; echo "RESULT: FAIL"; exit 1
fi
echo; echo "RESULT: PASS"
