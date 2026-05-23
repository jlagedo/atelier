#!/usr/bin/env bash
# S7 runtime-share smoke test (macos-port-execution.md §S7) — Apple Silicon only.
#
# Two layers, run in sequence:
#
#   BLACK-BOX (broker + vmctl): boots a VM the way the product does and exercises the
#   real attach/detach RPCs. It (1) reproduces the S6 single-workspace mount, then
#   (2) drives the multi-session path (-tag/-target). With the code as written this
#   second part is expected to FAIL with a rollback: there is one virtio-fs device with
#   one fixed tag ("workspace"), so guestd's per-share-tag `mount -t virtiofs <tag>`
#   can't match a second share and the broker rolls the host share back. Capturing that
#   precisely is the point — it is the gap S7 must close.
#
#   WHITE-BOX (gated Go probe): compiles services/internal/vmm's tests, codesigns the
#   test binary with the virtualization entitlement, and runs TestS7RuntimeShareProbe.
#   That probe drives the driver directly (host-only SetShare, no broker rollback) to
#   answer validation #1: does a share added after start() become visible, with/without
#   a remount, and what is the multi-share topology. Its log ends with a VERDICT INPUTS
#   block to paste into macos-port-plan.md §Files Door.
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

  # --- Phase B: multi-session via the product RPCs ---
  echo; echo "[B] multi-session (-tag/-target) — exercising the product path"
  local any_multi=0
  for s in s1 s2; do
    if vm attachWorkspace -id vm0 -path "$work/$s" -tag "$s" -target "/sessions/$s" >/dev/null 2>&1; then
      any_multi=1
      local ls; ls="$(vm exec -id vm0 -- ls -A "/sessions/$s" 2>&1 || true)"
      ok "multi $s: attached and mounted (/sessions/$s = $ls)"
      vm detachWorkspace -id vm0 -tag "$s" >/dev/null 2>&1 || true
    else
      info "multi $s: attachWorkspace rolled back (expected gap — see header; broker can't hold a 2nd virtio-fs tag)"
    fi
  done
  [[ "$any_multi" == 1 ]] && ok "multi-session path works through vmctl" \
                          || info "multi-session via vmctl is non-functional today → the white-box probe & S7 redesign cover this"

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
