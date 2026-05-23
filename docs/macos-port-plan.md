# macOS Port Plan

> **Status:** planning document, not an implemented path.  
> **Last updated:** 2026-05-22.  
> **Goal:** keep Atelier's containment model and product shape while replacing the
> Windows HCS substrate with a macOS Virtualization.framework substrate.

This plan starts from the current Windows implementation: a Go broker owns policy,
audit, VM lifecycle, files, network, and compute; the Electron main process talks to
that broker over Hop 2; the TypeScript agent loop runs inside the Linux guest.

The right macOS port is not a fork of the product. It is a new platform driver
below the same broker contract.

## Goals

- Run the same Electron UI and Session Manager on Windows and macOS.
- Keep the Go broker as the containment chokepoint for policy and audit.
- Keep the in-guest `guestd` protocol and the in-guest TypeScript agent loop.
- Preserve the "one shared VM, many session workspaces" product model if the macOS
  file-sharing primitives support it cleanly.
- Produce pinned OS/architecture-specific VM bundles instead of mixing guest images
  across hypervisors.

## Non-Goals

- Do not move privileged VM control into Electron main.
- Do not rewrite the broker in Swift just to call Virtualization.framework.
- Do not make the first macOS slice depend on full product packaging, notarization,
  or background-helper installation.
- Do not treat macOS NAT networking as a final containment story. It can be a spike
  convenience, but the product path needs host-mediated egress.

## Current Portability Boundary

The product-level contract is already mostly portable:

- Hop 1: renderer to Electron main through the narrow preload bridge.
- Hop 2: JSON-RPC 2.0 with Content-Length framing.
- Broker methods: `createVM`, `startVM`, `stopVM`, `exec`, `execInput`,
  `attachWorkspace`, `detachWorkspace`, `readFile`, `writeFile`,
  `setEgressPolicy`.
- Guest control plane: `guestd` JSON-RPC over a socket, with `exec/output`
  notifications.
- Agent loop: `packages/agent/src/cli-guest.ts` in `--serve` mode.

The non-portable pieces are concentrated below the broker's VMM seam:

- `services/internal/vmm/driver_windows.go` maps the platform-neutral driver to
  HCS concepts.
- `services/internal/hcs/doc.go` authors a Windows HCS/LCOW compute-system document.
- `services/internal/vmm/driver_windows.go` dials the guest with AF_HYPERV through
  `go-winio`.
- `services/internal/netjail/network.go` starts a gvisor-tap-vsock link over the
  Windows hvsocket transport.
- `services/cmd/guestd/mount_linux.go` mounts HCS-served Plan9/9p shares.
- `image/build.sh` currently produces an x86_64 Hyper-V bundle and converts the
  rootfs to VHD.
- `apps/desktop/src/main/sessions/manager.ts` still defaults to a Windows bundle
  path.

## Platform Mapping

| Capability | Windows Today | macOS Target |
|---|---|---|
| VM lifecycle | HCS via `computecore.dll` | Virtualization.framework `VZVirtualMachine` |
| Linux boot | HCS KernelDirect: kernel + initrd + VHD | `VZLinuxBootLoader`: kernel + initrd + RAW/ASIF root disk |
| Root disk | ext4 inside VHD, attached read-only | ext4 inside RAW or ASIF, attached read-only |
| Control plane | AF_HYPERV to guest `AF_VSOCK` | `VZVirtioSocketDevice.connect(toPort:)` to guest vsock port |
| Guest to host services | guest dials host CID 2 over vsock | `VZVirtioSocketListener` on host ports |
| Files | HCS Plan9/9p runtime shares | virtio-fs directory sharing |
| Network | gvisor-tap-vsock host network over hvsocket | preferred: host model/MCP proxy over vsock; parity option: file-handle NIC plus gVisor filter |
| Privilege/install | Windows service / LocalSystem | signed app/helper with `com.apple.security.virtualization`; helper managed by launchd/SMAppService if needed |

## Recommended Architecture

Keep `services` as the public host service and introduce a platform-neutral VMM
layer beneath the broker.

```text
services/internal/vmm/
  driver.go              # lifecycle, guest socket, share, network contracts
  manager.go             # cross-platform orchestration
  bundle.go              # resolves correct bundle for host OS/arch/hypervisor
  hyperv/                # current HCS document, computecore, hvsock, Plan9
  vz/                    # Go client for macOS helper, or a narrow native bridge
  stub/                  # unsupported/no-VM development driver

native/darwin/AtelierVMHelper/
  Package.swift
  Sources/AtelierVMHelper/
    main.swift
    VirtualMachineController.swift
    SocketBridge.swift
    VirtioFSController.swift
    NetworkAttachment.swift

image/bundles/
  linux-amd64-hyperv/
  linux-arm64-vz/
  linux-amd64-vz/
```

The cleanest implementation path is a small Swift helper for Virtualization.framework
and a Go-side `vmm/vz` client. Go should keep policy/audit and the external Hop 2
contract; Swift should only own the native VM object and return file descriptors or
small lifecycle events.

Avoid a large cgo/Objective-C bridge in the broker until proven necessary. A helper
keeps the native framework integration small, separately signed, and easier to
replace if Apple's VM APIs change.

## Driver Contract

The driver interface should speak Atelier concepts, not HCS concepts:

```go
type Driver interface {
    Create(ctx context.Context, cfg VMConfig) error
    Start(ctx context.Context, id string) error
    Stop(ctx context.Context, id string) error
    DialGuest(ctx context.Context, id string, port uint32) (net.Conn, error)
    AttachShare(ctx context.Context, id string, share ShareConfig) error
    DetachShare(ctx context.Context, id string, tag string) error
    StartEgress(ctx context.Context, id string, policy *netjail.Allowlist) (io.Closer, error)
}
```

`VMConfig` should become platform-neutral:

- `KernelPath`
- `InitrdPath`
- `RootDiskPath`
- `RootDiskFormat`: `vhd`, `raw`, `asif`
- `KernelCmdLine`
- `MemoryMB`
- `CPUCount`
- `Arch`
- `Hypervisor`: `hyperv`, `vz`

The Windows implementation can still author the HCS JSON internally. The macOS
implementation should never see HCS `ResourcePath`, Plan9 flags, or security
descriptors.

## Image Strategy

Build one guest rootfs recipe, but emit platform bundles.

On Apple Silicon, the primary bundle should be `linux-arm64-vz`. Virtualization.framework
runs guests of the same architecture as the host, so the current linux/amd64 bundle is
not the default macOS path. Rosetta support for Linux VMs can be evaluated later as a
compatibility feature, not as the base port.

Image build changes:

- Parameterize `ARCH`, Docker `--platform`, Go `GOARCH`, and Node dependency install.
- Emit raw ext4 for macOS instead of VHD, then optionally wrap/convert to ASIF.
- Keep kernel, initrd, modules, rootfs, `guestd`, and agent `node_modules` pinned as
  one bundle.
- Keep the read-only rootfs invariant.
- Keep separate bundle manifests per platform/arch.

Proposed bundle layout:

```text
image/bundle/
  windows-amd64-hyperv/
    vmlinuz
    initrd
    rootfs.vhd
    manifest.txt
  darwin-arm64-vz/
    vmlinuz
    initrd
    rootfs.raw
    manifest.txt
```

## Files Door on macOS

Use virtio-fs, not Plan9/9p, on macOS. The guest-side mount implementation should
become share-type aware:

- Hyper-V: current 9p over vsock fd mount.
- Virtualization.framework: `mount -t virtiofs <tag> <target>`.

Open spike: verify whether `VZVirtioFileSystemDevice.share` can be updated safely
for new per-session directories while the VM is running. If yes, preserve the current
one-VM/many-session model. If no, choose between:

- one virtio-fs device with a `VZMultipleDirectoryShare` provisioned before VM start,
- a shared host staging directory containing symlinks or session folders,
- one VM per active session on macOS,
- or a controlled VM restart when the set of live shares changes.

The broker's host-side file jail remains mandatory either way. The guest mount is for
compute convenience; the privileged boundary still mediates `readFile` and `writeFile`.

## Network Door on macOS

There are two plausible tracks.

Track A, recommended product path: move model and MCP traffic behind host-side proxies
available over vsock. The guest never receives `ANTHROPIC_API_KEY` and does not need
general internet. This aligns with `vm-hardening.md` C1/C2 and reduces the macOS
networking problem to a narrow protocol gateway.

Track B, parity path: use `VZFileHandleNetworkDeviceAttachment` to connect the VM's
virtio NIC to a host-managed datagram socket, then adapt the current gVisor/DNS/TCP
allowlist stack to that attachment. This best preserves the current "host is the whole
network" model. NAT can be used for early boot/testing only; it should not be the final
security posture because it grants broad indirect network reachability.

Avoid bridged networking unless a specific enterprise environment requires it. Apple's
bridged attachment needs an additional networking entitlement and is the wrong default
for containment.

## macOS Helper Shape

Use a minimal signed helper with the virtualization entitlement:

- owns `VZVirtualMachine` instances,
- starts/stops VMs,
- exposes guest socket connections as local file descriptors or framed streams,
- configures virtio-fs devices,
- configures network attachments,
- streams serial output to the Go broker logs.

The helper should not implement policy, file jails, provider credentials, or session
logic. Those stay in Go.

For development, run the helper manually from the terminal. For shipping, package it
inside the Electron app and register it with launchd/SMAppService only if background
service behavior is needed.

## Desktop Changes

Make the Electron main process platform-aware without teaching it hypervisor details.

- Rename TypeScript "pipe" wording to "host address" where practical.
- Default `ATELIER_HOST_PIPE`/address by platform:
  - Windows: `\\.\pipe\atelier-host`
  - macOS/Linux dev: `/tmp/atelier-host.sock` or an app-owned socket under user data.
- Resolve `ATELIER_BUNDLE_DIR` by platform and arch, rather than hardcoding a Windows
  path.
- Keep `SessionManager`'s lifecycle model unchanged unless the virtio-fs runtime-share
  spike forces a macOS-specific session policy.

## Milestone Plan

1. **Refactor without behavior change.** Keep the cross-platform orchestration in
   `internal/vmm` and keep the current HCS implementation behind the Windows driver.
   All existing Windows commands should still pass after this slice.
2. **Bundle resolver.** Add platform/arch bundle resolution and remove Windows default
   paths from Electron main.
3. **macOS boot spike.** Build a minimal Swift helper that boots a Linux kernel + initrd
   + raw rootfs, captures serial output, and shuts down cleanly.
4. **Guest control spike.** Connect from the macOS helper to `guestd` on
   `vsock.GuestRPCPort`, then make `vmctl exec -- python3 --version` work.
5. **Virtio-fs files.** Mount one workspace into the guest. Then test multi-session
   attach/detach semantics.
6. **Agent loop.** Run `cli-guest --serve` in the macOS VM and drive it from the
   existing desktop Session Manager.
7. **Network containment.** Prefer the host model proxy path; otherwise implement the
   file-handle NIC plus gVisor allowlist.
8. **Packaging.** Sign/notarize the helper and Electron app, wire entitlements, and add
   macOS install/run docs.

## Main Risks

- **Dynamic shares:** virtio-fs may not support the exact runtime add/remove behavior
  HCS Plan9 gives us today.
- **Networking parity:** gvisor-tap-vsock is tuned around vsock transports; the macOS
  file-handle NIC path needs a real spike.
- **Architecture split:** Apple Silicon wants arm64 guest artifacts. The agent, Node
  modules, `guestd`, and any native packages must be built for that target.
- **Helper signing:** Virtualization.framework requires the virtualization entitlement,
  so local dev, CI, and packaging must handle signing earlier than the Windows path did.
- **Credential residency:** passing provider keys into the guest is already called out
  as a hardening issue. The macOS port is a good moment to fix it rather than preserve it.

## References

- Apple Virtualization.framework:
  <https://developer.apple.com/documentation/virtualization>
- `VZVirtualMachine` and virtualization entitlement:
  <https://developer.apple.com/documentation/virtualization/vzvirtualmachine>
- Adding the virtualization entitlement:
  <https://developer.apple.com/documentation/virtualization/adding_the_virtualization_entitlement_to_your_project>
- `VZLinuxBootLoader`:
  <https://developer.apple.com/documentation/virtualization/vzlinuxbootloader>
- `VZDiskImageStorageDeviceAttachment`:
  <https://developer.apple.com/documentation/virtualization/vzdiskimagestoragedeviceattachment>
- `VZVirtioSocketDevice`:
  <https://developer.apple.com/documentation/virtualization/vzvirtiosocketdevice>
- `VZVirtioFileSystemDevice`:
  <https://developer.apple.com/documentation/virtualization/vzvirtiofilesystemdevice>
- `VZFileHandleNetworkDeviceAttachment`:
  <https://developer.apple.com/documentation/virtualization/vzfilehandlenetworkdeviceattachment>
- `VZNATNetworkDeviceAttachment`:
  <https://developer.apple.com/documentation/virtualization/vznatnetworkdeviceattachment>
- `SMAppService`:
  <https://developer.apple.com/documentation/servicemanagement/smappservice>
