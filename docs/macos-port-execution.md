# macOS Port — Execution Plan

> **Companion to [`macos-port-plan.md`](./macos-port-plan.md).** That doc decides *what*
> and *why* (architecture, API validation, framework mapping). This doc is the **execution
> tracker**: it cuts the port into thin, reviewable, demoable slices and records progress.
> **Last updated:** 2026-05-23 (S6 landed).

## How to use this doc

- Work is cut into **thin vertical slices**, same convention as
  [`implementation-status.md`](./implementation-status.md): a slice is the *smallest*
  change that adds an **observable capability** and leaves the system **runnable**. One
  slice ≈ one PR.
- Each slice lists **Goal · Work · Touches · Verify · Exit · Review · Depends · Risk**.
  `Verify` is a real command/observation, not a mock. `Exit` is the binary done-condition.
  `Review` is the PR reviewer's focused checklist.
- The path is **depth-first along the critical boot path** (compile → arm64 artifacts →
  boot → guest exec) before the three doors (files, network) and the product loop.
- Keep the **dashboard** below current: `☐` todo · `◐` in progress · `☑` done · `⊘` blocked.
- Slice IDs map to the milestone numbers in `macos-port-plan.md` §"Milestone Plan" via the
  **Plan ref** column, so the two docs stay reconcilable.

---

## Progress Dashboard

| ID | Slice | Plan ref | Status | PR |
|---|---|---|---|---|
| S0 | Driver seam (Driver/Manager, build tags) | M0 | ☑ `03a78ae` | — |
| S1 | darwin build-tag split + compiling stub | M3 (pre) | ☑ `cc094d6` | — |
| S2 | arm64 guest bundle (`darwin-arm64-vz`, raw ext4) | M2 | ☑ `2d2db72` | — |
| S3 | desktop bundle/arch resolution | M2 | ☑ `c0ca554` | — |
| S4 | dev signing + macOS boot spike (Create/Start/Stop) | M3 | ☑ | — |
| S5 | guest control plane — `DialGuest` + `exec` | M4 | ☑ | — |
| S6 | virtio-fs single workspace share | M5 | ☑ | — |
| S7 | runtime add/remove + multi-session smoke test | M5 | ☐ | |
| S8 | agent loop end-to-end (NAT crutch allowed) | M6 | ☐ | |
| S9 | network containment — vsock egress seam, drop NAT | M7 | ☐ | |
| S10 | packaging, notarization, install docs | M8 | ☐ | |

### Dependency order

```text
S0 ─ done
 └─ S1 ─ S2 ─ S3 ─ S4 ─ S5 ─┬─ S6 ─ S7 ─┐
                            └────────────┴─ S8 ─ S9 ─ S10
```

S2 and S3 (artifacts + desktop resolution) can land in parallel after S1. The critical
boot path is **S1 → S2 → S4 → S5**; S3 is needed before the desktop drives a real boot
(by S8). S6/S7 (files door) and S9 (network door) can be reviewed independently once S5
lands, but S8 (agent loop) needs S6 (at least one mounted workspace) and benefits from the
S4 NAT crutch for egress until S9 replaces it.

---

## Slices

### S1 — darwin build-tag split + compiling stub  ☑

- **Goal:** the broker and all Go packages **compile for `GOOS=darwin`** with a stub
  driver, before any framework code exists. This unblocks every later darwin slice.
- **Work:**
  - Narrow `driver_other.go` from `//go:build !windows` to `//go:build !windows && !darwin`.
  - Add `driver_darwin.go` (`//go:build darwin`) implementing the `Driver` interface as a
    stub returning `ErrUnsupported` (mirror `unsupportedDriver`), with `NewDriver` wired.
  - Confirm `vsock_other.go` / guestd `*_other.go` already cover darwin (host-side only;
    guest code is Linux-only and not built for darwin).
- **Touches:** `services/internal/vmm/driver_other.go`,
  `services/internal/vmm/driver_darwin.go` *(new)*.
- **Verify:** `GOOS=darwin GOARCH=arm64 go build ./...` from `services/` succeeds; existing
  Windows build and `go test ./internal/vmm/...` still pass.
- **Exit:** darwin compiles end-to-end with the stub driver; no behavior change on Windows.
- **Review:** build tags are mutually exclusive (no file built twice / never); stub returns
  `ErrUnsupported` from every method; `NewDriver` signature matches the contract.
- **Depends:** S0.
- **Risk:** Low. Pure build-tag plumbing.

---

### S2 — arm64 guest bundle (`darwin-arm64-vz`)  ☑

- **Goal:** `image/build.sh` emits a bootable **arm64** bundle with **raw ext4** rootfs —
  the hard prerequisite for any VZ boot (validation #7: x86_64 will not boot on Apple Silicon).
- **Work:**
  - Parameterize `ARCH` (`ARCH=${ARCH:-aarch64}` for this bundle).
  - rootfs `docker build --platform linux/arm64` so `node_modules` + apt kernel are arm64.
  - `GOARCH=arm64` for the `guestd` and `gvforwarder` build/install lines.
  - Emit `rootfs.raw` — skip the `qemu-img convert -O vpc` step; `cmd_rootfs` already leaves
    `$WORK/rootfs.ext4`, `cmd_bundle` already prefers it.
  - Write the bundle to `image/bundle/darwin-arm64-vz/` with `manifest.txt`; keep the
    existing `windows-amd64-hyperv/` layout untouched. Static `resolv.conf` is unchanged.
- **Touches:** `image/build.sh`, `image/Makefile` (if it hardcodes arch), `image/bundle/`.
- **Verify:** after a build, `image/bundle/darwin-arm64-vz/` contains
  `vmlinuz initrd rootfs.raw manifest.txt`; `file rootfs.raw` reports ext4;
  `file guestd`/`file gvforwarder` (or `go version -m`) report `arm64`.
- **Exit:** a reproducible arm64 raw-ext4 bundle exists; Windows bundle build unaffected.
- **Review:** no x86_64 leakage (check the kernel package + Go binaries are arm64); read-only
  rootfs invariant preserved; Windows path still produces a VHD.
- **Depends:** S0 (independent of S1, but sequenced after for tidy review).
- **Risk:** Medium. Cross-arch Docker build; kernel package must resolve to arm64.

---

### S3 — desktop bundle/arch resolution  ☑

- **Goal:** Electron main picks the right bundle and disk format **by platform**, with no
  hardcoded Windows paths.
- **Work:**
  - Replace the `E:\dev\atelier\image\bundle` default (`manager.ts:92`) with a
    platform/arch resolver; keep the `ATELIER_BUNDLE_DIR` override. macOS → app-owned
    `darwin-arm64-vz` (dev: repo-relative `image/bundle/darwin-arm64-vz`).
  - `rootfsPath` (`manager.ts:309`) → `rootfs.raw` on macOS, `rootfs.vhd` on Windows;
    `vmlinuz`/`initrd` names unchanged (`:307`/`:308`).
  - Default the host address by platform (Windows `\\.\pipe\atelier-host`; macOS/Linux dev a
    unix socket). Rename "pipe" wording → "host address" where low-risk.
- **Touches:** `apps/desktop/src/main/sessions/bundle.ts` *(new — pure resolver)*,
  `…/sessions/manager.ts`, `…/ipc/handlers.ts` (injects the platform base dir),
  `…/host-client/index.ts` (`defaultHostAddress`), `…/sessions/bundle.test.ts` *(new)*.
- **Verify:** unit/manual — on `process.platform === "darwin"` the resolver returns the
  `darwin-arm64-vz` dir + `rootfs.raw`; `ATELIER_BUNDLE_DIR` still overrides; Windows still
  resolves the existing path + `.vhd`.
- **Exit:** no `E:\…` or `.vhd` literal on the macOS path; Windows defaults intact.
- **Review:** override precedence (`opts → env → platform default`) preserved; no Windows
  regression; host-address default sane on each platform.
- **Depends:** S0 (artifacts from S2 needed only at runtime, S8).
- **Risk:** Low.
- **Landed:** pure `bundle.ts` (`bundleTarget`/`rootfsFileName`/`resolveBundleDir`) +
  `defaultHostAddress`; `handlers.ts` injects packaged-vs-dev base dir (`process.resourcesPath/bundle`
  is a placeholder until S10 packaging). `ATELIER_HOST_ADDR` added; `ATELIER_HOST_PIPE` kept for
  back-compat. Verified: `npm run typecheck && lint && test` green (16 tests).

---

### S4 — dev signing + macOS boot spike (Create/Start/Stop)  ☑

- **Goal:** boot the arm64 guest under `Virtualization.framework` from `driver_darwin.go`
  (Option A, `Code-Hex/vz`): kernel + initrd + raw rootfs **read-only**, capture serial,
  shut down cleanly.
- **Work:**
  - Add `github.com/Code-Hex/vz` dependency.
  - Implement `Create`: build `VZVirtualMachineConfiguration` —
    `VZLinuxBootLoader(KernelPath, InitrdPath)`,
    `VZDiskImageStorageDeviceAttachment(RootFSPath, readOnly: true)` (validation #6),
    one `VZVirtioSocketDevice`, one `VZVirtioFileSystemDevice` with an empty
    `VZMultipleDirectoryShare`. Don't `start()`.
  - Implement `Start`/`Stop` on **one serial dispatch queue** (validation #3); capture
    serial console to broker logs.
  - **Dev signing prerequisite:** ad-hoc codesign the broker with
    `com.apple.security.virtualization` under the hardened runtime — the framework refuses
    to start otherwise. Document the exact `codesign` command in the slice PR.
  - **NAT crutch:** attach `VZNATNetworkDeviceAttachment` (no entitlement, validation #4)
    purely to confirm liveness. Mark it clearly as removed in S9.
- **Touches:** `services/internal/vmm/driver_darwin.go`, `services/go.mod`/`go.sum`,
  a dev `entitlements.plist`, build/sign notes (root `README`/`AGENTS.md`).
- **Verify:** `vmctl createVM -id vm0 …` then `vmctl startVM -id vm0` boots the guest; serial
  log shows the kernel boot + login prompt; `vmctl stopVM -id vm0` exits cleanly. Re-run is
  idempotent.
- **Exit:** an arm64 guest boots and halts under VZ, driven through the unchanged Manager.
- **Review:** all VM calls confined to the single serial queue; rootfs attached
  `readOnly: true`; entitlement + hardened-runtime signing reproducible; NAT flagged as
  temporary; no HCS concepts leak into the darwin driver.
- **Depends:** S1, S2.
- **Risk:** High. First framework integration; signing/threading constraints; cgo build.
- **Landed:** `driver_darwin.go` implements Create/Start/Stop on `Code-Hex/vz` v3.7.1 —
  `VZLinuxBootLoader` (cmdline `console=hvc0 root=/dev/vda ro noresume init=/sbin/init`),
  read-only raw rootfs (validation #6), virtio-socket (for S5) + empty virtio-fs (for S6) +
  entropy devices, and the **NAT crutch** (validation #4, flagged for S9). Serial console
  captured to broker logs via `console_darwin.go` (os.Pipe → scanner, the darwin analog of
  `console_windows.go`). The binding owns the per-VM serial dispatch queue (validation #3),
  so no hand-rolled queue. Dev signing: `scripts/build-sign-darwin.sh` (protogen → cgo build
  → ad-hoc `codesign --options runtime` with the pre-existing
  `services/packaging/darwin/atelier-vm.entitlements`); requires Xcode CLT + `CGO_ENABLED=1`.
  - **Build-pipeline fix (S2 gap):** Ubuntu's arm64 `/boot/vmlinuz` is a **gzip** Image,
    which `VZLinuxBootLoader` refuses (Code=1, no serial). The arch was correct; only the
    packaging was. Fixed at the source — `image/kernel/fetch-kernel.sh` now gunzips the
    kernel to a decompressed arm64 `Image` for `DISK=raw` (VZ) targets while leaving the
    Windows compressed `vmlinuz` untouched (same `vmlinuz` filename, so S3's resolver is
    unchanged). Chosen over a runtime decompress in the driver: the darwin bundle is
    separate (no Windows risk), it happens once at build time, and the driver stays the
    clean platform seam.
  - **Verified:** boot reaches `/sbin/init` — serial shows `EXT4-fs (vda) … ro` and
    `atelier guest init: kernel 6.8.0-117-generic`; `stopVM` returns cleanly (`err=null`),
    `vmCount` → 0; create/start/stop is idempotent on a reused id. `go test/vet`, `gofmt`,
    and `GOOS=windows go build ./...` all green (vz excluded from non-darwin builds).
  - **S5 boundary surfaced:** `guestd` then fails `open /dev/vsock` and the guest panics —
    the guest loads the Hyper-V `hv_sock` transport (`image/guest/init.sh`), not VZ's
    virtio-vsock. That is exactly S5's "guest control plane" work, out of S4 scope.

---

### S5 — guest control plane — `DialGuest` + `exec`  ☑

- **Goal:** reach `guestd` over vsock and run a command in the guest.
- **Work:**
  - Implement `DialGuest` via `VZVirtioSocketDevice.connect(toPort:)` to
    `vsock.GuestRPCPort` (5000); adapt `VZVirtioSocketConnection` → `net.Conn` (validation #8,
    host CID 2 / guest CID 3).
  - Confirm the broker's exec path is transport-agnostic over the returned `net.Conn`.
- **Touches:** `services/internal/vmm/driver_darwin.go`,
  `services/internal/vsock/vsock_other.go` (note: Plan9 ports unused on macOS).
- **Verify:** `vmctl exec -id vm0 -- python3 --version` prints the version from inside the
  guest; a streamed command relays `exec/output` and the exit code.
- **Exit:** arbitrary guest exec works over the VZ vsock from the existing broker RPC.
- **Review:** the `net.Conn` adapter handles half-close/EOF and concurrent dials; no leaked
  connections; queue discipline preserved for the connect call.
- **Depends:** S4.
- **Risk:** Medium. vsock connection lifecycle + framing over the adapter.
- **Landed:** two small changes; the whole exec path downstream was already
  transport-agnostic over the `net.Conn` from `DialGuest`, so no broker/RPC/vmctl/guestd
  code changed.
  - **Host** (`driver_darwin.go`): `DialGuest` uses the `*vz.VirtioSocketDevice` cached on
    `Start` and calls `Connect(port)` — `*vz.VirtioSocketConnection` already satisfies
    `net.Conn`, so it's returned directly (no adapter). A bounded retry
    (`dialGuestRetries=40` × `dialGuestRetryWait=250ms`, ~10s) loops on the
    `*vz.NSError` `ECONNRESET` that `Connect` returns until `guestd` binds its listener;
    any other error is terminal, and `ctx` is honored between attempts. No hand-rolled
    queue — the binding marshals `Connect` onto the device's own dispatch queue
    (validation #3).
  - **Guest** (`image/guest/init.sh`): the S4 boundary was the guest loading only the
    Hyper-V `hv_sock` transport, so `guestd` panicked on `open /dev/vsock`. Fixed by also
    `modprobe vmw_vsock_virtio_transport` (additive + tolerant `|| true`, so the Hyper-V
    bundle is unaffected — its virtio modprobe just no-ops). That module pulls in the
    `vsock` core, registering `/dev/vsock`. Doc-only: `vsock_linux.go`'s comment is now
    transport-neutral.
  - **Verified end-to-end** on Apple Silicon: rebuilt the `darwin-arm64-vz` bundle (the
    kernel ships `vmw_vsock_virtio_transport.ko`), re-signed the broker, then
    `createVM`/`startVM`/`exec`/`stopVM`. Serial shows `NET: Registered PF_VSOCK protocol
    family` then `atelier-guestd listening transport=vsock port=5000` (no more
    `/dev/vsock` panic). `exec` ran `uname -a` (aarch64), `id` (non-root `atelier`
    uid 1001), `python3 --version`, and a `sh -c "exit 7"` whose exit code propagated
    correctly. `stopVM` clean, `vmCount → 0`. `go test/vet`, `gofmt`, and
    `GOOS=windows go build ./...` all green (vz excluded from non-darwin builds).
  - **S9 boundary surfaced (not a regression):** `gvforwarder` restart-loops trying to
    reach the host egress listener at `vsock://2:1024` because darwin `StartEgress` is
    still `ErrUnsupported`; the S4 NAT crutch carries real egress meanwhile. That's S9's
    "network containment" work, out of S5 scope.

---

### S6 — virtio-fs single workspace share  ☑

- **Goal:** mount **one** host workspace into the running guest via virtio-fs (the files door).
- **Work:**
  - `AttachWorkspace`: add `share.HostPath` to the device's
    `VZMultipleDirectoryShare.directories` keyed by `share.Tag`; **ignore `share.Port`**
    (virtio-fs is tag-addressed, not vsock-port-addressed).
  - `DetachWorkspace`: remove `share.Tag` from the set.
  - Guest side: `mount_linux.go` becomes share-type aware — `mount -t virtiofs <tag> <target>`
    when a virtio-fs device for the tag is present (e.g. under `/sys/bus/virtio`), else fall
    back to the existing 9p path (validation #2). Validate the tag (`validateTag`) before use.
- **Touches:** `services/internal/vmm/driver_darwin.go`,
  `services/cmd/guestd/mount_linux.go`.
- **Verify:** `vmctl attachWorkspace -id vm0 -tag ws0 -host <dir>` then
  `vmctl exec -id vm0 -- ls <mountpoint>` lists the host directory's contents;
  `detachWorkspace` removes it.
- **Exit:** one workspace mounts read/write per policy and unmounts cleanly under VZ.
- **Review:** tag validated; 9p fallback intact for Windows guests; `Port` confirmed unused;
  host file jail still mediates `readFile`/`writeFile` (guest mount is convenience only).
- **Depends:** S5.
- **Risk:** Medium. Guest mount branch + tag rules (charset/length undocumented).
- **Landed (2026-05-23):** the planned `AttachWorkspace` hit a **binding gap** — Apple's
  framework exposes runtime virtio-fs share mutation (`VZVirtioFileSystemDevice.share`
  get/set, macOS 12+, via `VZVirtualMachine.directorySharingDevices`), but `Code-Hex/vz`
  v3.7.1 (and upstream `main`) only expose the **config-time** `SetDirectoryShare`; the
  `internal/objc` package is unimportable and `*vz.VirtualMachine` hides its raw pointer, so
  the runtime accessor can't be added with a local shim.
  - **Fork** (`github.com/jlagedo/vz`, cloned to `~/Developer/vz`, branch
    `feat/runtime-directory-share` off `v3.7.1`): added `VirtualMachine.DirectorySharingDevices()`
    + runtime `VirtioFileSystemDevice.SetShare` (mirrors `SocketDevices`; `setShare` runs on the
    VM's serial dispatch queue). Wired via `replace github.com/Code-Hex/vz/v3 =>
    /Users/jlagedo/Developer/vz` in `services/go.mod`. **Upstream PR deferred** until the change
    is exercised more broadly (the `replace` + fork are the interim).
  - **Host** (`driver_darwin.go`): caches the runtime `fsdev` on `Start` (next to `socket`),
    tracks an authoritative `shares map[string]*vz.SharedDirectory` (the device has no readable
    getter), and on attach/detach rebuilds the whole share and swaps it with `fsdev.SetShare`.
    `buildShare`: 1 entry → `SingleDirectoryShare` (clean `/workspace`), else
    `MultipleDirectoryShare`. `share.Port` ignored; `validateShareTag` (non-empty, <36, charset).
  - **Guest** (`mount_linux.go` + `init.sh`): `mountShare` chooses transport at **runtime** —
    `mount -t virtiofs <tag>` when `virtiofs` ∈ `/proc/filesystems`, else falls back to the
    9p-over-vsock path (Hyper-V unaffected). `init.sh` adds a tolerant `modprobe virtiofs`.
  - **Verified end-to-end** on Apple Silicon: rebuilt the `darwin-arm64-vz` bundle + re-signed
    broker; `attachWorkspace` mounts the host dir as `workspace on /workspace type virtiofs (rw)`;
    host contents readable; a **live** host write after mount is visible in the guest; guest write
    visible on host; `detachWorkspace` unmounts; **re-attach is idempotent**; clean `stopVM`.
    Serial: `virtiofs virtio3` probe → `guestd listening` → `mounted share … tag=workspace` →
    `unmounted share`. `GOOS=windows` still builds (replace is darwin-cgo-only).
  - **Deferred to S7:** the single-vs-multiple-share topology verdict and the "share added after
    `start()` visible without a remount" smoke test (S6 sidesteps it — the guest mounts *after* the
    host `SetShare`, so the mount is the nudge).

---

### S7 — runtime add/remove + multi-session smoke test  ☐

- **Goal:** resolve the plan's **single remaining unverified spike** (validation #1): does the
  guest see a share **added after `start()`** without a remount nudge, and does
  attach/detach work for multiple concurrent sessions?
- **Work:**
  - Smoke-test live add: with the VM running, `AttachWorkspace` a new tag, then check guest
    visibility — with and without a guest-side `mount -t virtiofs` nudge.
  - Exercise the multi-session attach/detach path (several tags up/down on one live VM).
  - **Record the verdict in `macos-port-plan.md`** (§Files Door open spike). If live add
    needs a remount, the host-adds-then-guest-mounts shape is the documented primary; the
    fallbacks (staging symlinks → controlled restart → one-VM-per-session) are the ladder.
- **Touches:** `services/internal/vmm/driver_darwin.go` (only if a nudge RPC is needed),
  `services/cmd/guestd/mount_linux.go`, `docs/macos-port-plan.md` (verdict).
- **Verify:** a scripted run attaches/detaches ≥3 workspaces on a live VM; each becomes
  visible/invisible in the guest as expected; result documented.
- **Exit:** runtime-share behavior is **known and documented**, and the chosen shape is
  implemented (preserving the one-VM/many-session model, or its documented fallback).
- **Review:** the verdict is written down with the exact reproduction; chosen fallback (if
  any) doesn't silently drop the one-VM model without calling it out.
- **Depends:** S6.
- **Risk:** Medium–High. The one genuinely unverified Apple behavior in the whole port.

---

### S8 — agent loop end-to-end  ☐

- **Goal:** run `cli-guest --serve` in the macOS VM and drive a real turn from the existing
  desktop Session Manager.
- **Work:**
  - Wire the desktop (now resolving the `darwin-arm64-vz` bundle from S3) to create/start the
    VM, mount a workspace (S6), and run the agent loop in `--serve` mode.
  - Egress may still ride the **NAT crutch** from S4 at this stage so the agent can reach the
    model; containment lands in S9.
- **Touches:** `apps/desktop/src/main/sessions/manager.ts` (runtime path),
  `packages/agent/src/cli-guest.ts` (only if a platform assumption surfaces).
- **Verify:** from the desktop in WORK mode, a session boots the macOS VM, mounts a workspace,
  and completes one agent turn with streamed output.
- **Exit:** the full product loop runs on Apple Silicon (with NAT egress still permitted).
- **Review:** no hypervisor details leaked into Electron main; session lifecycle unchanged
  from Windows unless S7 forced a macOS-specific policy (call it out if so).
- **Depends:** S5, S6 (S7 informs session policy), S3.
- **Risk:** Medium. First full top-to-bottom integration.

---

### S9 — network containment — vsock egress seam, drop NAT  ☐

- **Goal:** replace the NAT crutch with the existing gvisor-tap-vsock jail re-hosted over a
  VZ vsock listener — the network door, reusing all jail logic.
- **Work:**
  - Abstract the transport: change `netjail.Start` (currently `Start(log, filter)` at
    `network.go:70`, calling `egressListenURL()` internally at `:115`/`:294`) to **accept a
    host-supplied `net.Listener`**. Windows passes its hvsock listener; macOS passes a
    VZ-backed one. Downstream (`http.Serve`, switch, DNS/DHCP/forwarder) is unchanged.
  - `StartEgress` (darwin): register a `VZVirtioSocketListener` on `vsock.EgressLinkPort`
    (1024), adapt each `VZVirtioSocketConnection` → `net.Conn`, expose as a `net.Listener`,
    hand it to `netjail.Start`, and return the `*netjail.Network` as the `io.Closer` the
    Manager tracks.
  - Verify the guest side ports unchanged (`gvforwarder` dials `CID2:1024` either way).
  - **Remove the NAT attachment** from `Create` (S4).
- **Touches:** `services/internal/netjail/network.go`,
  `services/internal/vmm/driver_windows.go` (pass its listener in),
  `services/internal/vmm/driver_darwin.go`.
- **Verify:** with NAT removed, `vmctl setEgressPolicy` allows an allowlisted host from inside
  the guest and a non-allowlisted host is denied; DNS resolves only allowlisted names.
- **Exit:** the guest has **no real NIC**; all egress flows through the jail over VZ vsock;
  Windows egress unchanged.
- **Review:** `netjail.Start` signature change doesn't regress Windows; the
  `VZVirtioSocketListener`→`net.Listener` adapter handles accept errors/backpressure; NAT
  fully removed; `io.Closer` wiring matches Manager expectations.
- **Depends:** S5 (vsock), S8 (so the agent loop exercises real egress).
- **Risk:** Medium. The adapter + the shared `netjail.Start` signature change touch Windows.

---

### S10 — packaging, notarization, install docs  ☐

- **Goal:** ship the macOS port: proper signing/notarization, entitlements wired into the
  build, Electron app packaged, and install/run docs.
- **Work:**
  - Production codesign + **notarize** the broker (Option A) with
    `com.apple.security.virtualization` under the hardened runtime; sign the Electron app and
    embed the bundle.
  - Decide Option A (signed broker) vs Option B (Swift helper) for shipping; only build the
    helper (`macOS Helper Shape` in the plan) if a least-privilege split or background
    lifecycle (launchd/SMAppService) demands it.
  - Add macOS install/run docs; update `docs/README.md`,
    `implementation-status.md`, and this dashboard.
- **Touches:** desktop packaging config, signing/notarization scripts, `entitlements.plist`,
  `docs/` (install guide + status), optionally a Swift helper target.
- **Verify:** a notarized, Gatekeeper-passing build launches on a clean Apple Silicon Mac and
  completes an agent turn (the S8 demo) without dev signing.
- **Exit:** a distributable macOS build runs the full loop with the containment posture from S9.
- **Review:** entitlements minimal and correct; notarization reproducible in CI; no provider
  credential-residency regression beyond the documented (deferred) gap.
- **Depends:** S9.
- **Risk:** Medium. Notarization/CI signing friction.

---

## Definition of Done (the port)

The port is "booted" (the plan's stated priority) when **S1–S6** pass: an arm64 guest boots
under VZ, `guestd` is reachable, one workspace mounts, and the agent loop is drivable. It is
"shipped" when **S1–S10** pass: containment is back on the vsock jail (NAT removed) and a
notarized build runs the full loop on a clean machine.

## Out of scope (tracked in the plan, not here)

Deferred to post-port per `macos-port-plan.md` §"Major features deferred until after the
port": host MITM proxy + per-boot ephemeral CA, dynamic multi-session shares at scale beyond
S7's first validated path, Rosetta amd64 guest binaries, credential-residency hardening, ASIF
rootfs, and the Option B launchd/SMAppService lifecycle.
