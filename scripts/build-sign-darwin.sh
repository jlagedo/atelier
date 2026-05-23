#!/usr/bin/env bash
# Build + sign the Atelier Go host binaries on macOS (the darwin analog of
# scripts/build-go.ps1). Virtualization.framework refuses to run unless the
# process that instantiates VZVirtualMachine is codesigned with
# com.apple.security.virtualization under the hardened runtime, so the broker
# (cmd/host) must be (re)signed after every build. cgo invalidates the signature
# on each rebuild — re-run this script whenever you rebuild.
#
# Requirements: macOS on Apple Silicon, Xcode Command Line Tools (cgo toolchain).
#
# Usage: ./scripts/build-sign-darwin.sh
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

entitlements="services/packaging/darwin/atelier-vm.entitlements"

echo "==> regenerating protocol bindings"
npm run protogen

echo "==> building host + vmctl (cgo)"
CGO_ENABLED=1 go -C services build -o bin/ ./cmd/host ./cmd/vmctl

# Ad-hoc sign (-) the broker only — it is the Mach-O that instantiates the VM.
# vmctl is a plain RPC client and needs no entitlement.
echo "==> codesigning services/bin/host with the virtualization entitlement"
codesign --force --sign - \
  --options runtime \
  --entitlements "$entitlements" \
  services/bin/host

echo "==> verifying signature"
codesign --display --entitlements - services/bin/host

echo "built + signed services/bin/host (+ vmctl)"
