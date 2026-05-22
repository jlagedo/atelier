#!/usr/bin/env pwsh
# Build the Atelier Go host binaries into services/bin/ (see README "Build & run"):
# regenerate the protocol bindings (the Go build needs the generated protocol.go),
# then build the broker (host) + dev CLI (vmctl). guestd is cross-compiled into the
# rootfs by image/build.sh, not here.

$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $true

Push-Location (Resolve-Path (Join-Path $PSScriptRoot ".."))
try {
  npm run protogen
  go -C services build -o bin/ ./cmd/host ./cmd/vmctl
  Write-Host "built services\bin\host.exe + vmctl.exe" -ForegroundColor Green
}
finally {
  Pop-Location
}
