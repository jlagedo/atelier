# macOS Port Plan

> **Status:** in progress. Milestone 1 (the `internal/vmm` Driver seam) has landed
> (`03a78ae`). The macOS driver, image, and desktop wiring are not yet implemented.  
> **Last updated:** 2026-05-23.  
> **Goal:** keep Atelier's containment model and product shape while replacing the
> Windows HCS substrate with a macOS Virtualization.framework substrate.  
> **Priority right now:** make the *current* code run on Apple Silicon as-is. Get a
> guest booting, `guestd` reachable, one workspace mounted, and the agent loop driven
> from the existing Session Manager. Anything larger (host MITM proxy, dynamic
> multi-session shares, Rosetta amd64 compatibility, credential-residency fixes) is
> explicitly deferred until after the port boots.

This plan starts from the current Windows implementation: a Go broker owns policy,
audit, VM lifecycle, files, network, and compute; the Electron main process talks to
that broker over Hop 2; the TypeScript agent loop runs inside the Linux guest.

The right macOS port is not a fork of the product. It is a new platform driver
below the same broker contract.

The architecture choices below are cross-checked against Anthropic's own
macOS implementation (see [`claude-cowork-internals.md`](./claude-cowork-internals.md)),
which ships exactly this shape — Ubuntu guest under `Virtualization.framework`,
vsock control plane, VirtioFS file sharing, host-mediated egress — and against
direct validation of the Apple framework APIs (see
[Framework API Validation](#framework-api-validation)).

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
| Network | gvisor-tap-vsock host network over hvsocket | same gvisor-tap-vsock jail, re-hosted over a `VZVirtioSocketListener` (port 1024); no real NIC — see [Network Door](#network-door-on-macos) |
| Privilege/install | Windows service / LocalSystem | signed broker (Option A) or helper (Option B) with `com.apple.security.virtualization`; helper via launchd/SMAppService if needed |

## Framework API Validation

Each technique below was validated against current Apple documentation. Verdicts
drive the decisions in the rest of this plan.

| # | Claim | Verdict | Notes / source |
|---|---|---|---|
| 1 | **Filesystem shares can be changed while the VM runs** | **VERIFIED (with smoke-test caveat)** | `VZVirtioFileSystemDevice.share` is `get/set` on the *runtime* device (macOS 12+), reachable via `VZVirtualMachine.directorySharingDevices`; `VZMultipleDirectoryShare.directories` is also mutable. Apple does not document *live guest visibility* of a post-`start()` swap in prose, so confirm by smoke test — but the API supports it. **This resolves the plan's biggest open spike** (see [Files Door](#files-door-on-macos)). |
| 2 | **Guest mounts a share with `mount -t virtiofs <tag> <target>`** | VERIFIED | `<tag>` = `VZVirtioFileSystemDeviceConfiguration.tag`. A `validateTag(_:)` exists; exact length/charset rules are not published — validate tags before use. |
| 3 | **Virtualization.framework is callable from Go/Node, not just Swift** | VERIFIED | `com.apple.security.virtualization` must be on whichever Mach-O instantiates `VZVirtualMachine`, run under the hardened runtime. `Code-Hex/vz` drives the whole framework from Go via cgo. No separate daemon is required. `VZVirtualMachine(configuration:queue:)` requires a **serial dispatch queue** — all VM ops must run on that one queue. |
| 4 | **NAT needs no special networking entitlement; only bridged does** | VERIFIED | `VZNATNetworkDeviceAttachment` explicitly does not require `com.apple.vm.networking`; only `VZBridgedNetworkDeviceAttachment` does. So a NAT early-boot spike is unblocked by entitlements. |
| 5 | **`VZLinuxBootLoader` takes an explicit kernel + initrd + cmdline** | VERIFIED (S4) | Host supplies `vmlinuz` directly; no in-image bootloader needed. `VZEFIBootLoader` is an alternative (macOS 13+). **Atelier's existing bundle already pins `vmlinuz`+`initrd`, so it maps to `VZLinuxBootLoader` with no boot-chain changes** — simpler than Cowork's opaque VZ boot. **Caveat found in S4:** on Apple Silicon the kernel must be a *decompressed* arm64 `Image`; Ubuntu's `/boot/vmlinuz` is a gzip Image that VZ refuses (Code=1, no serial), so `image/kernel/fetch-kernel.sh` gunzips it for `DISK=raw` targets. The cmdline also differs from HCS: `console=hvc0` (virtio console) and `root=/dev/vda` (virtio-blk), not `ttyS0`/`sda`. |
| 6 | **`VZDiskImageStorageDeviceAttachment` attaches a raw image read-only** | VERIFIED | `init(url:readOnly:)`, macOS 11+; accepts raw ext4 images; read-only is supported → preserves the read-only-rootfs invariant. ASIF (Apple Sparse Image Format) is macOS 26+; raw is the right choice now. |
| 7 | **An x86_64 guest cannot run on Apple Silicon; the VM is host-native** | VERIFIED | Rosetta only translates *user-mode* x86_64 binaries inside an **arm64** guest (`VZLinuxRosettaDirectoryShare`). **The current `linux/amd64` bundle will not boot under VZ on Apple Silicon — an arm64 guest bundle is mandatory** (see [Image Strategy](#image-strategy)). |
| 8 | **`VZVirtioSocketDevice` gives host↔guest vsock** | VERIFIED | Host→guest: `connect(toPort:)`. Guest→host: host registers `VZVirtioSocketListener` via `setSocketListener(_:forPort:)`. Host CID = 2, guest CID = 3. Maps directly to `DialGuest` (port 5000) and the egress link (guest dials host on port 1024). |

### What Cowork confirms, and where Atelier differs

- **Confirms:** the whole substrate — Ubuntu 22.04 guest, `VZVirtualMachine`, vsock
  control plane, VirtioFS sharing, and *host-mediated* egress (the guest has no
  general network). Atelier's `netjail` already implements that egress model.
- **Cowork mounts on demand:** its `mountPath(sessionId, hostPath, name, mode)` adds
  folders to a running guest at `/sessions/<name>/mnt/<folder>` — the live-share
  behavior validated in #1. Atelier's one-VM/many-session model is therefore viable.
- **Differs (deliberately):** Cowork puts VM control in a Swift Node addon
  (`@ant/claude-swift`) loaded *inside Electron main*. Atelier keeps VM control in the
  privileged Go broker (non-goal: no VM control in Electron main). So Atelier
  implements the `Driver` seam in Go (cgo) or behind a thin helper — not in the UI
  process.
- **Deferred to post-port (Cowork has, Atelier will add later):** the per-boot
  ephemeral-CA MITM proxy and domain allowlist as a *protocol gateway*; Atelier's
  current `netjail` already does DNS-allowlist + IP-pinned TCP forwarding, which is
  enough to boot. The MITM/model-proxy layer is a [major feature](#major-features-deferred-until-after-the-port).

## Recommended Architecture

The platform-neutral seam already exists (M1, `03a78ae`). The current layout is flat,
selected by Go build tags — not the subpackage tree this plan first sketched:

```text
services/internal/vmm/
  driver.go              # Driver interface + VMConfig + WorkspaceShare (platform-neutral)
  manager.go             # cross-platform orchestration (tracks live VMs, shares, egress)
  driver_windows.go      # //go:build windows  — HCS/hvsock/Plan9 implementation
  driver_other.go        # //go:build !windows — ErrUnsupported stub  ← split for darwin
  console_windows.go     # serial console capture (Windows)
```

The macOS port adds **`driver_darwin.go`** (`//go:build darwin`) and narrows the stub's
tag to `//go:build !windows && !darwin`. `NewDriver(*slog.Logger) Driver` returns the
darwin driver under that tag. No change to `manager.go`, the broker, or the Hop 2
contract.

### Two validated ways to implement `driver_darwin.go`

API validation #3 confirms both are viable. Pick by friction, not aesthetics.

- **Option A — cgo via `Code-Hex/vz` (recommended for the first working slice).**
  Implement the `Driver` interface directly in Go using the mature `Code-Hex/vz`
  binding (it drives the full framework: `VZVirtualMachine`, `VZLinuxBootLoader`,
  `VZVirtioSocketDevice`, `VZVirtioFileSystemDevice`, network attachments). No second
  process, no IPC hop, no Swift toolchain in the build. Cost: the **broker binary
  itself** must be codesigned with `com.apple.security.virtualization` under the
  hardened runtime, and all VM calls must run on a single serial dispatch queue (the
  binding handles the queue, but it constrains threading). This is the shortest path to
  "boot the guest as-is."
- **Option B — thin Swift helper + Go client.** A minimal signed helper owns
  `VZVirtualMachine`; the Go `driver_darwin.go` is an RPC client to it. Better process
  isolation and independent signing/replacement, at the cost of an extra hop and a
  Swift build. Reserve this for when packaging/signing or a least-privilege split
  argues for it — not for the first boot.

Go keeps policy, audit, the file jail, and the external Hop 2 contract in both options.
The non-goal stands: VM control never moves into Electron main. Note that "no large
cgo bridge until proven necessary" is now weighed against a mature off-the-shelf
binding — Option A is small *because* `Code-Hex/vz` already wrote the bridge.

## Driver Contract

This is the **committed** seam (`services/internal/vmm/driver.go`), already speaking
Atelier concepts rather than HCS concepts — the macOS driver implements it verbatim:

```go
type Driver interface {
    Create(ctx context.Context, cfg VMConfig) error
    Start(ctx context.Context, id string) error
    Stop(ctx context.Context, id string) error
    DialGuest(ctx context.Context, id string, port uint32) (net.Conn, error)
    AttachWorkspace(ctx context.Context, id string, share WorkspaceShare) error
    DetachWorkspace(ctx context.Context, id string, share WorkspaceShare) error
    StartEgress(ctx context.Context, id string, filter *netjail.Allowlist) (io.Closer, error)
}
```

Committed `VMConfig`: `ID`, `KernelPath`, `InitrdPath`, `RootFSPath`, `MemoryMB`,
`CPUCount`.
Committed `WorkspaceShare`: `HostPath`, `ReadOnly`, `Tag`, `Port`.

How the macOS driver maps each method:

| Method | macOS implementation |
|---|---|
| `Create` | Build `VZVirtualMachineConfiguration`: `VZLinuxBootLoader(KernelPath, InitrdPath)`; `VZDiskImageStorageDeviceAttachment(RootFSPath, readOnly: true)`; one `VZVirtioSocketDevice`; one `VZVirtioFileSystemDevice` (empty `VZMultipleDirectoryShare`). Don't `start()` yet. |
| `Start` | `VZVirtualMachine.start()` on the serial queue; install the egress vsock listener (port 1024). |
| `Stop` | `stop()` / `requestStop()`; release the queue. |
| `DialGuest` | `VZVirtioSocketDevice.connect(toPort:)` — `port` is `vsock.GuestRPCPort` (5000). |
| `AttachWorkspace` | Add `share.HostPath` to the running device's `VZMultipleDirectoryShare.directories` under `share.Tag` (validation #1). **`share.Port` is ignored** — virtio-fs is tag-addressed, not vsock-port-addressed (that field is the Windows 9p vsock port). |
| `DetachWorkspace` | Remove `share.Tag` from the directory set. |
| `StartEgress` | Bridge the guest's vsock egress link (port 1024) into the existing `netjail.Network` (see [Network Door](#network-door-on-macos)). Return its `io.Closer`. |

**Deltas from this plan's first draft** (intentional, do not "fix" the code to match
the old sketch): the committed `VMConfig` is leaner — no `RootDiskFormat`,
`KernelCmdLine`, `Arch`, or `Hypervisor` field. The macOS driver infers raw format and
host-native arch. Add those fields only if a second macOS format/arch path actually
needs them; today they would be dead config. The Windows driver still authors HCS JSON
internally; the macOS driver never sees HCS `ResourcePath`, Plan9 flags, or security
descriptors.

## Image Strategy

Build one guest rootfs recipe, but emit platform bundles.

Per validation #7, **Apple Silicon requires an arm64 guest** — the current bundle is
x86_64 and will not boot under VZ. This is a hard blocker for the first slice, not a
later compatibility nicety. Rosetta (amd64 user binaries inside the arm64 guest) is a
[deferred feature](#major-features-deferred-until-after-the-port).

`image/build.sh` today hardcodes the amd64 path. The concrete changes:

- `ARCH="x86_64"` → parameterize (`ARCH=${ARCH:-aarch64}` for the macOS bundle).
- The rootfs `docker build` must run `--platform linux/arm64` so the baked
  `node_modules` and apt-installed kernel are arm64.
- The two `go build`/`go install` lines that produce `guestd` and `gvforwarder`
  currently pass `GOARCH=amd64` → make them `GOARCH=arm64` for this bundle.
- The matched kernel package (`linux-image-generic-hwe-22.04`) resolves to the arm64
  kernel under the arm64 build; `vmlinuz`/`initrd` extraction is unchanged. The boot
  path stays direct (`VZLinuxBootLoader`), so no GRUB/EFI work (validation #5).
- **Emit raw ext4, not VHD.** `cmd_rootfs` already leaves `$WORK/rootfs.ext4` when
  `qemu-img` is absent, and `cmd_bundle` already pins `rootfs.ext4` if present — so the
  macOS path is mostly *skipping* the `qemu-img convert -O vpc` step and naming the
  output `rootfs.raw`. `VZDiskImageStorageDeviceAttachment` takes the raw image as-is
  (validation #6); ASIF conversion is optional and macOS 26+ only.
- Keep the read-only-rootfs invariant (attach `readOnly: true`).
- Keep kernel, initrd, modules, rootfs, `guestd`, and agent `node_modules` pinned as
  one bundle, with separate per-platform manifests.
- The guest's static `resolv.conf` (`nameserver 192.168.127.1`, baked in `cmd_rootfs`)
  matches `netjail`'s gateway and is platform-neutral — no change.

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

Use virtio-fs, not Plan9/9p, on macOS.

**Host side:** one `VZVirtioFileSystemDevice` carrying a `VZMultipleDirectoryShare`.
`AttachWorkspace`/`DetachWorkspace` mutate the share's `directories` set on the running
device, keyed by `WorkspaceShare.Tag` (validation #1). The `Port` field is unused on
this path.

**Guest side:** the in-guest mount must become share-type aware. `guestd`'s
`mount_linux.go` currently does the Windows 9p-over-vsock mount; on macOS the same
guest must instead run `mount -t virtiofs <tag> <target>` (validation #2). The guest
cannot see the host OS, so branch on what's actually present: if a virtio-fs device
matching the tag exists (e.g. under `/sys/bus/virtio`), mount virtiofs; otherwise fall
back to 9p. (Alternatively, have the broker pass the share type in the `attachWorkspace`
guestd RPC — cleaner, but a protocol change.)

**Open spike (downgraded):** validation #1 confirms the *API* supports runtime share
changes; what Apple does not document is whether the **guest sees a share added after
`start()` without a remount nudge**. Smoke-test this early. If live add fails, the
fallbacks are, in order of preference:

- mount the new tag in the guest on demand after the host adds it (most likely the real
  shape — host adds directory, guest runs `mount -t virtiofs`),
- a shared host staging directory of per-session symlinks under one fixed share,
- a controlled VM restart when the live-share set changes,
- one VM per active session (last resort — drops the one-VM model).

The broker's host-side file jail remains mandatory either way. The guest mount is for
compute convenience; the privileged boundary still mediates `readFile` and `writeFile`.

## Network Door on macOS

**Key realization from the current code:** Atelier already implements the "host is the
whole network" jail that Cowork ships, and it's *not* NIC-based. In
`internal/netjail/network.go`, the guest has **no real NIC** — its `gvforwarder`
(gvisor-tap-vsock's `cmd/vm`) dials a host listener **over vsock** and bridges a tap
device to it. The host runs a full user-mode TCP/IP stack with a default-deny DNS
resolver (allowlisted names only, IPs pinned) and a TCP forwarder that dials only
pinned IPs. That *is* the containment story; it does not need porting, only re-hosting
its transport.

The one Windows-specific line is the transport URL:

```go
// egressListenURL() hardcodes Hyper-V's hvsock GUID template:
// vsock://<port-8hex>-FACB-11E6-BD58-64006A7986D3
```

So the macOS network work is narrow and concrete:

1. **Abstract the egress transport.** Change `netjail.Start` to accept a host-supplied
   `net.Listener` instead of calling `egressListenURL()` itself. Windows passes its
   hvsock listener; macOS passes a VZ-backed one. Everything downstream (`http.Serve`,
   the switch, DNS/DHCP/forwarder) is unchanged.
2. **macOS `StartEgress`** registers a `VZVirtioSocketListener` on
   `vsock.EgressLinkPort` (1024), adapts each accepted `VZVirtioSocketConnection` to
   `net.Conn`, exposes them as a `net.Listener`, and hands that to `netjail.Start`. It
   returns the `netjail.Network` as the `io.Closer` the Manager already tracks.
3. **Guest side is likely unchanged.** Inside the Linux guest, the Hyper-V link already
   appears as `AF_VSOCK` to host CID 2; under VZ the guest also sees `AF_VSOCK` to
   CID 2. `gvforwarder` dials `CID2:1024` and POSTs `/connect` either way, so
   `guestd`'s egress bring-up should port without changes — verify in the boot spike.

This makes the first macOS slice reuse the entire egress jail with one transport seam.

**Early-boot convenience only — NAT.** While wiring the vsock egress bridge, you can
give the guest a real NIC via `VZNATNetworkDeviceAttachment` to validate boot, apt, and
DNS quickly. It needs no `com.apple.vm.networking` entitlement (validation #4) — but it
**bypasses the jail**, so it is strictly a spike crutch, never a shipped posture. Remove
it once the vsock bridge works.

**Product direction (deferred).** Cowork additionally fronts model/MCP traffic with a
per-boot ephemeral-CA MITM proxy and never puts `ANTHROPIC_API_KEY` in the guest
(`vm-hardening.md` C1/C2). That protocol-gateway layer is a
[major feature](#major-features-deferred-until-after-the-port); the netjail allowlist is
sufficient to boot. Avoid bridged networking entirely unless an enterprise environment
demands it (it needs `com.apple.vm.networking` and is the wrong default for containment).

## macOS Helper Shape

This section describes **Option B** (separate Swift helper). It is *not* needed for the
first working slice — Option A (cgo via `Code-Hex/vz`) implements the `Driver` in-process
and skips the helper. Build the helper only when packaging/signing or a least-privilege
split justifies the extra hop.

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
`apps/desktop/src/main/sessions/manager.ts` has two hardcoded Windows assumptions:

- `bundleDir` defaults to `String.raw\`E:\dev\atelier\image\bundle\`` (line ~92).
  Replace with a platform/arch default and keep the `ATELIER_BUNDLE_DIR` override:
  Windows → the existing path; macOS → an app-owned `darwin-arm64-vz` bundle dir under
  user data (dev: a repo-relative `image/bundle`).
- The VM config sets `rootfsPath: path.join(bundleDir, "rootfs.vhd")` (line ~309).
  On macOS this must be `rootfs.raw` (the disk format the VZ driver attaches). Select by
  platform; `vmlinuz`/`initrd` names are unchanged.
- Rename TypeScript "pipe" wording to "host address" where practical; default the host
  address by platform — Windows `\\.\pipe\atelier-host`, macOS/Linux dev a unix socket
  (`/tmp/atelier-host.sock` or an app-owned socket under user data).
- Keep `SessionManager`'s lifecycle model unchanged unless the virtio-fs runtime-share
  smoke test forces a macOS-specific session policy.

## Make the Current Code Run on macOS

This is the priority deliverable: the minimal, file-by-file change set to boot the
*existing* product on Apple Silicon. No new product features — just port what's there.

| File / area | Change | Why |
|---|---|---|
| `services/internal/vmm/driver_other.go` | Narrow build tag `//go:build !windows` → `//go:build !windows && !darwin` | Free up `darwin` for the real driver; other Unixes keep the stub |
| `services/internal/vmm/driver_darwin.go` *(new)* | Implement `Driver` (Option A: cgo via `Code-Hex/vz`). Map per the [Driver Contract](#driver-contract) table | The whole macOS substrate |
| `services/internal/netjail/network.go` | Make `Start` take a host-supplied `net.Listener`; stop calling `egressListenURL()` internally | Reuse the egress jail under VZ vsock instead of Hyper-V hvsock |
| `services/cmd/guestd/mount_linux.go` | Branch share type: `mount -t virtiofs <tag> <target>` when a virtio-fs device is present, else current 9p | Guest mounts macOS shares |
| `services/internal/vsock/*` | Note Plan9 ports (`WorkspacePlan9Port`, `SessionPlan9PortBase`) are unused on macOS; `WorkspaceShare.Port` ignored by the VZ driver | virtio-fs is tag-addressed, not vsock-port-addressed |
| `image/build.sh` | `ARCH=aarch64`, `docker build --platform linux/arm64`, `GOARCH=arm64` for guestd+gvforwarder, emit `rootfs.raw` (skip `qemu-img -O vpc`) | arm64 guest is mandatory under VZ (validation #7) |
| `apps/desktop/src/main/sessions/manager.ts` | Platform default for `bundleDir`; `rootfsPath` → `rootfs.raw` on macOS; host address → unix socket | Drop hardcoded `E:\…` and `.vhd` |
| Signing | Codesign the broker (Option A) or helper (Option B) with `com.apple.security.virtualization`, hardened runtime; run VM ops on one serial dispatch queue | Framework refuses to start otherwise (validation #3) |

What this slice deliberately leaves alone: the broker's RPC contract, `manager.go`, the
agent loop, the Windows driver, and the netjail jail logic (only its transport seam moves).

## Milestone Plan

0. **Driver seam (DONE, `03a78ae`).** `internal/vmm` Driver/Manager landed; Windows
   behind `driver_windows.go`, `driver_other.go` stub. Windows behavior unchanged.
1. ~~**Refactor without behavior change.**~~ Superseded by milestone 0 — already done.
   Keep the cross-platform orchestration in `internal/vmm` and the HCS implementation
   behind the Windows driver. All existing Windows commands still pass.
2. **arm64 bundle + resolver.** Make `image/build.sh` emit `darwin-arm64-vz` (raw ext4,
   arm64 guestd/gvforwarder); add platform/arch bundle resolution and remove the
   Windows default path from `manager.ts`.
3. **macOS boot spike.** In `driver_darwin.go` (Option A, `Code-Hex/vz`), boot the
   kernel + initrd + raw rootfs read-only via `VZLinuxBootLoader` +
   `VZDiskImageStorageDeviceAttachment`, capture serial output, shut down cleanly.
   Use NAT only to confirm the guest is alive; remove it after.
4. **Guest control.** `DialGuest` via `VZVirtioSocketDevice.connect(toPort:)` to
   `vsock.GuestRPCPort` (5000); make `vmctl exec -- python3 --version` work.
5. **Virtio-fs files.** One `VZVirtioFileSystemDevice` + `VZMultipleDirectoryShare`;
   mount one workspace; then **smoke-test the runtime add/remove** (the only unverified
   bit of validation #1) and the multi-session attach/detach path.
6. **Agent loop.** Run `cli-guest --serve` in the macOS VM and drive it from the
   existing desktop Session Manager.
7. **Network containment.** Abstract `netjail.Start`'s transport; wire a
   `VZVirtioSocketListener` on `vsock.EgressLinkPort` (1024) into the existing
   gvisor-tap-vsock jail. Drop the NAT crutch. No new jail logic.
8. **Packaging.** Sign/notarize (broker for Option A, or helper for Option B) and the
   Electron app, wire entitlements, add macOS install/run docs.

## Major features deferred until after the port

These are real product gaps versus Cowork, but none block booting the current code.
Tackle them only once milestones 0–8 pass:

- **Host MITM proxy + per-boot ephemeral CA** as a model/MCP protocol gateway, so the
  guest never holds `ANTHROPIC_API_KEY` (`vm-hardening.md` C1/C2; Cowork's egress model).
  The current `netjail` allowlist is enough to boot.
- **Dynamic multi-session shares at scale** beyond the first validated mount — if the
  runtime-add smoke test (milestone 5) needs a fallback, that redesign lands here.
- **Rosetta for amd64 guest binaries** (`VZLinuxRosettaDirectoryShare`) as a
  compatibility feature — not the base port (validation #7).
- **Credential-residency hardening** — fix passing provider keys into the guest rather
  than preserving the Windows behavior.
- **ASIF rootfs** (macOS 26+) if sparse-image benefits justify it over raw.
- **launchd/SMAppService** background-helper lifecycle, if Option B ships.

## Main Risks

- **Dynamic shares (downgraded):** the API supports runtime add/remove (validation #1).
  The only residual unknown is whether the guest sees a share added after `start()`
  without a remount nudge — a milestone-5 smoke test, with documented fallbacks.
- **Networking transport seam:** the gvisor-tap-vsock jail is reused as-is; the risk is
  the small adapter that turns VZ's `VZVirtioSocketListener` connections into the
  `net.Listener` `netjail.Start` expects, plus decoupling the hardcoded Hyper-V hvsock
  URL. This is not a file-handle-NIC rebuild.
- **Architecture split:** Apple Silicon needs arm64 guest artifacts. The agent, Node
  modules, `guestd`, and `gvforwarder` must all be built for arm64 — a hard boot blocker,
  not a nicety.
- **Signing + threading:** the framework refuses to run without
  `com.apple.security.virtualization` under the hardened runtime, so dev/CI/packaging
  must sign earlier than Windows did. Option A also constrains the broker to drive the VM
  on a single serial dispatch queue (cgo) — design the darwin driver around that.
- **Credential residency (deferred):** passing provider keys into the guest is a known
  hardening gap; fixing it is a [post-port feature](#major-features-deferred-until-after-the-port),
  not part of the first boot.

## References

- Cowork macOS substrate (cross-check): [`claude-cowork-internals.md`](./claude-cowork-internals.md)
- `Code-Hex/vz` — Go/cgo binding for Virtualization.framework (Option A):
  <https://github.com/Code-Hex/vz>
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
- `VZVirtioFileSystemDevice.share` (runtime mutation):
  <https://developer.apple.com/documentation/virtualization/vzvirtiofilesystemdevice/share>
- `VZMultipleDirectoryShare`:
  <https://developer.apple.com/documentation/virtualization/vzmultipledirectoryshare>
- `VZVirtioSocketListener`:
  <https://developer.apple.com/documentation/virtualization/vzvirtiosocketlistener>
- Running Intel binaries in Linux VMs with Rosetta:
  <https://developer.apple.com/documentation/virtualization/running-intel-binaries-in-linux-vms-with-rosetta>
- `VZFileHandleNetworkDeviceAttachment`:
  <https://developer.apple.com/documentation/virtualization/vzfilehandlenetworkdeviceattachment>
- `VZNATNetworkDeviceAttachment`:
  <https://developer.apple.com/documentation/virtualization/vznatnetworkdeviceattachment>
- `SMAppService`:
  <https://developer.apple.com/documentation/servicemanagement/smappservice>
