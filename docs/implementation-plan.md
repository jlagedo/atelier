# Atelier — Implementation Plan

> **Companion to [`design.md`](design.md).** That doc decides *what* and *why*; this one
> decides *in what order* and *how to know each step works*. Section references like
> "§8" point at `design.md`.
>
> **Status:** active. **Last updated:** 2026-05-20.

---

## How to use this doc

- The work is cut into **thin vertical slices**. A slice is the *smallest* change that
  adds an **observable capability** and leaves the system **runnable**. One slice ≈ one PR.
- **Don't start a slice until the previous slice's Exit criteria are met.** No half-built
  layers waiting on each other.
- We go **depth-first along the critical path** (HCS boot → guest bridge) before breadth.
  Breadth (the three doors, the UI) comes after the substrate exists.
- **"Vertical" early ≠ "reaches the UI."** `design.md` §6 accepts that Electron is the
  *last* milestone, not the first. So early slices are vertical through the stack *that
  exists at that point* — each ends in a real, demoable command from the **terminal /
  `vmctl`**, not a mock. Once the bridge lands (M2), slices become genuinely
  feature-vertical: each of the **three doors** (§10) is its own slice.
- Each slice below lists: **Goal · Work · Touches · Verify · Exit · Depends · Risk.**
- Keep the **status box** at the top of each phase current: `☐` todo · `◐` in progress · `☑` done.

---

## Baseline — what already exists (verified 2026-05-20)

The scaffold went **wide and shallow**: every layer has a seam, almost no depth.

| Area | State |
|---|---|
| Go RPC (Hop 2) — JSON-RPC 2.0 + Content-Length framing, server/client/codec | **Real**, tests green |
| Broker / policy gate / audit log | **Real seam**; `getStatus` works, capability methods are gated stubs |
| `internal/hcs`, `internal/vm`, `internal/netjail`, `cmd/guestd` | **Stubs / empty** |
| Protocol codegen (`protocol.json` → TS + Go) | **Real**, zero-dep, regenerates |
| Image build | **Partial** — rootfs stage real; **kernel + initrd are TODO** |
| Desktop shell (hardened renderer, CSP, narrow IPC, one channel) | **Real shell**, no host-client wiring |
| Agent loop | **Empty** (README only) |

**Env confirmed on the dev box:** `hcsdiag.exe` + `wsl.exe` present (virtualization on);
Go installed but **not on PATH** (`C:\Program Files\Go\bin`); **Node v24**; **Docker not installed**.

**Net:** nothing on the critical path (HCS → guest bridge) exists yet. That is what Phase 1 attacks.

---

## Pivotal unknowns — resolve before/within M0–M1

These are **not** settled in `design.md` and gate the early slices:

1. ~~**hcsshim's UVM-boot code lives in `internal/`** → Go forbids importing it from our
   module.~~ → **RESOLVED in S0a (2026-05-20): (a) roll our own thin `vmcompute.dll`
   bindings + author the JSON doc.** Confirmed both blockers empirically: the internal-import
   rule (uvmboot only builds *inside* hcsshim) **and** that hcsshim's `uvm` LCOW path is
   welded to Microsoft's GCS guest (so vendoring/shelling-out can't boot our own-agent
   guest). (b)/(c) rejected. Doc template captured from hcsshim `makeLCOWDoc`. See S0a
   Result + §16.
2. **Image build can't run on Windows** (`build.sh` is bash; needs `docker` + `mke2fs`,
   neither on the host). → Run it **inside WSL**. Decided in **S0.2**.
3. **Kernel sourcing is unresolved** (`fetch-kernel.sh` is a TODO). → **Decouple** it from
   "drive HCS": first boot with a *built-in-driver* kernel (LCOW/WSL2, no initrd), then
   swap to the matched generic-Ubuntu kernel+initrd as a separate slice (**S1.2 → S1.3**).

---

## Phase overview

| Phase | Milestones (§14) | Theme | Demoable from |
|---|---|---|---|
| **0** | — | Dev-environment unblock | shell |
| **1** | M0–M2 | **Compute substrate**: boot a VM + exec bridge | `vmctl` |
| **2** | M3–M4 | **The doors**: workspace files + egress jail | `vmctl` |
| **3** | M5a–M5b | **The agent**: SDK loop, host then in-guest | Node CLI |
| **4** | M6 | **The product**: Electron shell + ship | Electron |

Cross-cutting through every phase: grow `packages/protocol/schema/protocol.json` as new
methods appear (`exec`, mount params, …) and **regenerate** (`npm run protogen`); never
hand-edit generated TS/Go. Add Zod emission when M3/M5 first need validated params.

---

## Phase 0 — Dev-environment unblock

> Status: `☑ S0.1` `☑ S0.2`

Tiny but blocking. No product code; just make the toolchain usable.

### S0.1 — Toolchain + HCS access
- **Goal:** Go usable from the shell; HCS callable without per-run elevation.
- **Work:** put `C:\Program Files\Go\bin` on PATH; add the dev user to **Hyper-V
  Administrators** (`aka.ms/hcsadmin`, §9); confirm **Virtual Machine Platform** is enabled.
- **Verify:** `go version` works in a fresh shell; `hcsdiag list` runs **without**
  "insufficient privileges".
- **Exit:** both commands succeed unelevated.
- **Depends:** —  **Risk:** group change may need a logoff/reboot.

### S0.2 — Image build host (WSL)
- **Goal:** `image/build.sh` runnable.
- **Work:** run the build inside WSL; install `docker` + `e2fsprogs` (`mke2fs`) there, or
  swap `docker export` for `debootstrap`. `qemu-img` for the VHD conversion.
- **Verify:** `image/build.sh check` reports all tools `ok`.
- **Exit:** `image/build.sh rootfs` produces an ext4 image (full rootfs is S1.1).
- **Depends:** —  **Risk:** docker-in-WSL setup; or commit to debootstrap path.

---

## Phase 1 — Compute substrate (M0–M2)

> Status: `☑ S0a` `☑ S1.1` `☑ S1.2` `☑ S1.3` `☑ S2.1` `☑ S2.2`
>
> The heart of the project. End state: a command on the host runs a program **inside our
> own Linux VM** and streams the output back. *"M0–M2 alone will teach you more about
> Cowork than almost anyone outside Anthropic"* (§14).

### S0a — M0: Boot *someone else's* UVM (spike)
- **Goal:** prove HCS works on the box **and** pick the HCS-access strategy (unknown #1).
- **Work:** boot **any** known-good kernel+initrd UVM and get guest output. Kernel source:
  LCOW pair / WSL2 kernel / (bootstrap only) Cowork's `vmlinuz`+`initrd` from
  `%APPDATA%\Claude\vm_bundles\`. Try strategy **(c)** first (prebuilt `uvmboot`); record
  whether **(a)** own-bindings or **(b)** vendor is the M1 path.
- **Touches:** throwaway `cmd/uvmboot` or notes; `go.mod` (+ `Microsoft/hcsshim` if vendoring).
- **Verify:** `hcsdiag list` (elevated) shows a **Running `VM`**; guest `uname -a` prints.
- **Exit:** a Linux guest shell reached from a Go program; **M1 access strategy chosen** and
  written into this doc + §16.
- **Depends:** S0.1.  **Risk:** kernel/initrd sourcing; hcsshim internal-import constraint.
- **Result (2026-05-20):** **HCS proven on the box.** Built hcsshim's `uvmboot`
  (`go install …/internal/tools/uvmboot@latest`, v0.14.1) and booted via it elevated:
  `HcsCreateComputeSystem` + `Start` succeed (`SystemCreateCompleted` → `SystemStartCompleted`),
  and our UVM shows as **`Running`** in `hcsdiag list` (`uvmboot-0`, both WSL- and
  Cowork-sourced kernel+initrd). **Strategy chosen: (a) our own thin `vmcompute.dll`
  bindings + our own compute-system JSON doc** — see §16 for the full rationale. Key
  learning that forced (a): hcsshim's LCOW path is **hard-wired to Microsoft's GCS**
  (cmdline tail `-- -e 1 /bin/vsockexec -e 109 /bin/gcs …`; expects guest callbacks on
  entropy/log hvsockets), so any non-gcs guest (WSL, Cowork, *and our own rootfs*) crashes
  ~1s in (`SystemCrashInitiated`) and emits nothing on the configured serial pipe. uvmboot
  is therefore **not** a usable shortcut for our guest. **Captured the doc template** from
  hcsshim `makeLCOWDoc`: SchemaVersion 2.1 · `VirtualMachine.Chipset.LinuxKernelDirect`
  {KernelFilePath, InitRdPath, KernelCmdLine} · `Devices.{Scsi, HvSocket, Plan9}` — we
  replicate this minus the gcs/vsockexec cmdline, pointing init at **our** `/sbin/init`.
  **"Guest shell / `uname` output" deferred to S1.2**, where our own doc boots our own
  rootfs with a console we control (it can't be demoed through gcs-locked uvmboot).
  Spike artifacts under `.spike/` (uvmboot, boot.ps1, probes) — disposable.

### S1.1 — M1: Build *our* rootfs
- **Goal:** an Ubuntu 22.04 ext4 root disk we control (§7).
- **Work:** `image/build.sh rootfs` in WSL — `docker export ubuntu:22.04` → `mke2fs -d`
  (no root) → `qemu-img` → `rootfs.vhd`. Install `image/guest/init.sh` as `/sbin/init`.
- **Touches:** `image/` (rootfs already scaffolded; verify the manifest in `rootfs/Dockerfile`).
- **Verify:** a `rootfs.vhd` exists; loop-mount in WSL shows `/usr/bin/python3`, `/sbin/init`.
- **Exit:** reproducible rootfs build artifact.
- **Depends:** S0.2.  **Risk:** ext4 sizing; VHD footer format (`vpc` vs VHDX).
- **Result (2026-05-20):** done as part of the S0.2 run — `bundle/rootfs.vhd` (325 MB, `vpc`)
  + raw `.work/rootfs.ext4` (2 GB). Verified **against the built image** with `debugfs`
  (no mount/sudo): `/usr/bin/python3` → `python3.10`; `/sbin/init` (via `sbin`→`usr/sbin`
  usrmerge symlink) is our guest init script; `/workspace` mount point present; full
  Ubuntu 22.04 userland. Verify script: `.spike/verify_rootfs.sh`.

### S1.2 — M1: Drive HCS yourself (first boot of our rootfs)
- **Goal:** **our** VM boots **our** rootfs, via **our** code. The central milestone.
- **Work:** implement `internal/hcs` (replace the stub) + `internal/vm`: author the
  compute-system **JSON doc** (KernelDirect, ext4 root on VHD, `console=hvc0 …`),
  `HcsCreateComputeSystem` + `Start`. **De-risk:** use a *built-in-driver* kernel
  (LCOW/WSL2) so **no initrd** is needed yet. Wire broker `createVM`/`startVM`/`stopVM`
  to it (replace the gated stubs).
- **Touches:** `internal/hcs/hcs_windows.go`, `internal/vm/*`, `internal/broker/broker.go`,
  `protocol.json` (CreateVM doc field already present).
- **Verify:** `vmctl createVM` + `startVM`; serial console (`hvc0`) shows the boot;
  `cat /etc/os-release` = Ubuntu 22.04; `python3 --version` works.
- **Exit:** our VM, our userland, started by our broker.
- **Depends:** S0a (strategy), S1.1.  **Risk:** the JSON doc shape; root-disk attach;
  whether the chosen kernel boots our rootfs with no initrd.
- **Result (2026-05-20): DONE — our VM booted our rootfs via our code.** Built our own
  **`computecore.dll`** bindings (`internal/hcs/computecore_windows.go`): the documented
  operation-based API — `HcsCreateOperation`/`HcsCreateComputeSystem`/`HcsWaitForOperationResult`
  /`Start`/`Terminate`/`Close` + `HcsGrantVmAccess`, via `golang.org/x/sys/windows` +
  `syscall.SyscallN`. **Chose `computecore.dll` over `vmcompute.dll`** because only the former
  exports the async operation surface (`HcsWaitForOperationResult` blocks for completion → no
  callbacks, no polling; vmcompute.dll lacks it — probed on the box). `MakeLCOWDoc` authors the
  schema-2.1 doc (no gcs/vsockexec tail; `init=/sbin/init`); `internal/vm.Manager` builds the
  doc, drives the driver, and bridges COM1→named-pipe console; broker `createVM`/`startVM`/`stopVM`
  now real (through the policy gate + audit); `vmctl` gained `-id/-kernel/-rootfs/-mem/-cpu`.
  Booted the **WSL2 built-in-driver kernel** (`6.6.114.1-microsoft-standard-WSL2`, **no initrd**)
  with `rootfs.vhd` SCSI-attached as **`/dev/sda`**, cmdline
  `console=ttyS0,115200 root=/dev/sda rw init=/sbin/init`. Serial log:
  `hv_storvsc → [sda] 2.00 GiB → EXT4-fs (sda) mounted r/w → Run /sbin/init →` **our** init banner
  (`atelier guest init: scaffold …`), with the `/workspace` 9p mount declining gracefully (no
  Plan9 share until S3.1); clean `stopVM`. `os-release=Ubuntu 22.04` + `python3.10` are established
  **transitively** (S1.1 `debugfs` verified that exact image; this boot mounted it and ran our init
  from it) — live interactive proof lands with the exec bridge (**S2.2**). Empirical wins: HCS
  COM-port pipe = **host-as-server** (`winio.ListenPipe`, HCS connects in); `HcsGrantVmAccess(id, vhd)`
  is **required** for the VM-worker virtual account to open the disk; built-in-driver kernel boots a
  VHD root with no initrd, as designed. Binaries `.spike/bin/{host,vmctl}.exe`; runner
  `.spike/boot_ours.ps1` (disposable spike harness).

### S1.3 — M1: Matched kernel + initrd (the real §7 image)
- **Goal:** replace the borrowed kernel with the **generic-Ubuntu kernel + matching
  initramfs**, kept coupled to `/lib/modules/<ver>` in the rootfs (§7 coupling rule).
- **Work:** implement `kernel/fetch-kernel.sh` (fetch the generic kernel + its modules) and
  `build.sh initrd` (`mkinitramfs` against that version, drivers from `initrd/modules.conf`).
  Pin `vmlinuz`/`initrd`/`rootfs` with `.origin` sha256 (Cowork's bundle discipline). Add
  `SetInitrdPath` to the compute-system doc.
- **Touches:** `image/kernel/fetch-kernel.sh`, `image/build.sh` (`cmd_initrd`, `cmd_bundle`),
  `internal/hcs` (initrd path).
- **Verify:** VM boots on the matched kernel; `uname -r` matches `/lib/modules`; a module
  loads (`modprobe` succeeds).
- **Exit:** a pinned, self-built `kernel + initrd + rootfs` bundle that boots.
- **Depends:** S1.2.  **Risk:** initramfs missing a driver → no root mount; module mismatch.
- **Result (2026-05-20): DONE — our VM boots the matched, self-built kernel+initrd+rootfs bundle.**
  Chose the **Docker-integrated** path so the coupling rule holds *by construction*: the rootfs
  Docker build installs `linux-image-generic-hwe-22.04` + `initramfs-tools`
  (`image/rootfs/Dockerfile`), so the matched **kernel (`6.8.0-117-generic`, Cowork-parity)**,
  its `/lib/modules/<ver>`, and a **full** boot initramfs (`initramfs-tools` default
  `MODULES=most`, like Cowork's fat initrd) all come from one apt transaction. `image/build.sh`
  refactored: a memoized `ensure_tree` builds+exports the container once; `kernel`/`initrd`
  **extract + pin** `vmlinuz`/`initrd` from `/boot` of that tree (`fetch-kernel.sh` rewritten to
  extract, not download); ext4 bumped 2G→4G for the modules; `manifest.txt` records the kernel
  version; `cmd_all` reordered `rootfs→kernel→initrd→bundle`. `image/initrd/modules.conf` is now
  reference-only (every driver is already in the full initrd). Go side: threaded an optional
  `initrdPath` through `protocol.json` (regenerated) → broker `CreateVMParams` → `vm.VMConfig` →
  `hcs.DocConfig.InitrdPath` (the doc field `LinuxKernelDirect.InitRdPath` was already wired in
  S1.2) → `GrantVMAccess` (VM-worker account reads the initrd); `vmctl` gained `-initrd`. Empty
  `initrdPath` preserves the S1.2 no-initrd boot (regression-safe). **Empirical boot
  (`.spike/boot_ours.ps1`, elevated):** serial log shows `Linux version 6.8.0-117-generic` →
  initrd unpacked → `hv_vmbus`/`hv_storvsc` from the initrd → `[sda] … 4.00 GiB` →
  `EXT4-fs (sda): mounted filesystem … r/w` (root on `/dev/sda`, no UUID) → switch_root → our
  `/sbin/init`, which prints **`kernel 6.8.0-117-generic | /lib/modules: 6.8.0-117-generic`**
  (the coupling proof: `uname -r` == the modules dir == `manifest.txt`) and
  **`modprobe ok (module ecosystem matched)`**; `9pnet`/`9p` load and the `/workspace` mount
  declines gracefully (no Plan9 share until S3.1); clean `stopVM` (`err:null`), no panic.
  **Two fixes the real initrd forced (not needed under S1.2's built-in-driver kernel):**
  (1) **`noresume`** added to the default cmdline (`internal/hcs/doc.go`) — Ubuntu's initramfs
  otherwise stalls boot ~30s in `local-premount` waiting for a non-existent hibernate/resume
  device; (2) **idempotent pseudo-fs mounts** in `image/guest/init.sh` — initramfs-tools moves
  `/proc`,`/sys`,`/dev` into the real root on switch_root, so our init's re-`mount` returned
  "already mounted" (exit 32) and under `set -e` killed PID 1 → kernel panic; the mounts now
  tolerate it (`2>/dev/null || true`). `go build ./...`/`vet`/`test` green. Spike runner extended
  with `-Initrd` (defaults to the bundle); binaries `.spike/bin/{host,vmctl}.exe`.

### S2.1 — M2: Guest daemon (hvsocket server side)
- **Goal:** an in-VM agent that accepts commands over vsock and streams stdout.
- **Work:** implement `cmd/guestd`: AF_VSOCK RPC server reusing `internal/rpc` (JSON-RPC +
  Content-Length); one method `exec` → run a command, emit stdout/stderr as **JSON-RPC
  notifications** (§8 streaming = notifications). `init.sh` execs `guestd` instead of `sh`.
- **Touches:** `cmd/guestd/main.go`, `internal/vm` (guest transport), `image/guest/init.sh`,
  rootfs manifest (ship the `guestd` binary).
- **Verify:** boot logs show `guestd` listening on the vsock port.
- **Exit:** guest daemon up at boot.
- **Depends:** S1.2 (a booting VM).  **Risk:** static-linking `guestd` for the rootfs;
  vsock port/CID conventions.
- **Result (2026-05-20): DONE — our guest daemon comes up on the vsock port at boot.**
  Implemented the guest side end-to-end (host side is S2.2). **`internal/rpc` gained a server→client notification
  path** (`notify.go`: `Notifier` + ctx helpers; `server.go`: a per-connection `connWriter`
  that serializes whole Content-Length messages, injected into the handler ctx) so a handler
  can stream while it runs — none existed before. New leaf package **`internal/vsock`** holds
  the one shared `GuestRPCPort = 5000` (host reaches it in S2.2 via the AF_HYPERV GUID
  `00001388-facb-11e6-bd58-64006a7986d3`) + a Linux `Listen()` over **`github.com/mdlayher/vsock`**
  (returns a `net.Listener`, plugs straight into `rpc.Server.Serve`) and a non-Linux stub so
  `go build ./...` stays green on the Windows box. **`cmd/guestd`** (was a scaffold) now binds
  vsock, serves `exec` (streams stdout/stderr as `exec/output` notifications, returns
  `{exitCode}`), and is robust as PID 1 (never `os.Exit`s — on listen failure it logs and
  blocks so the serial console stays readable, no kernel panic). Image: **`build.sh`**
  cross-compiles guestd (`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`, static) inside `ensure_tree`
  and installs it to `/usr/sbin/guestd` like `init.sh` (`go` added to the tool checks);
  **`init.sh`** now `modprobe hv_sock` then `exec /usr/sbin/guestd` (falls back to a shell if
  absent). `exec`'s wire shapes are deliberately **not** in `protocol.json` yet — they land in
  S2.2 with the host caller. **Verified:** `go build ./...` (native + `GOOS=linux`), `go vet`,
  `go test` all green, incl. a new `rpc` test asserting two notifications arrive before the
  response, in order. **Empirical boot (`.spike/boot_ours.ps1`, elevated):** serial console shows
  `EXT4-fs (sda): mounted … r/w` → our init (`kernel 6.8.0-117-generic`, `modprobe ok`) →
  `NET: Registered PF_VSOCK protocol family` + `hv_vmbus: registering driver hv_sock` →
  `atelier guest init: starting guestd …` →
  **`{"msg":"atelier-guestd listening","transport":"vsock","port":5000}`**, then the guest
  holds PID 1 (no kernel panic) through a clean `stopVM` (`err:null`).

### S2.2 — M2: Host↔guest exec bridge (Hop 3)
- **Goal:** **the** Phase-1 payoff — run a guest command from the host and stream output.
- **Work:** host `vm.RPCClient` over **AF_HYPERV** (`Microsoft/go-winio` hvsock); broker
  `exec` method → policy gate → guest `exec`; relay notifications back over Hop 2; add
  `vmctl exec`.
- **Touches:** `internal/vm` (host hvsock client), `internal/broker` (`exec`), `cmd/vmctl`,
  `protocol.json` (`exec` method + params).
- **Verify:** `vmctl exec -- ls -la /` streams the guest's output to the terminal,
  end-to-end across Hop 2 → Hop 3.
- **Exit:** **host drives the guest.** Substrate complete.
- **Depends:** S2.1.  **Risk:** hvsock connect handshake; back-pressure on streaming.
- **Result (2026-05-20): DONE — the host drives the guest; the Phase-1 substrate is complete.**
  Implemented the host half of Hop 3. **`internal/rpc` gained `Client.CallStream`** — the client
  twin of S2.1's server-side notifier: it delivers each interleaved JSON-RPC notification to a
  callback, then returns the response (the old single-shot `Call` is untouched). Covered by a new
  `net.Pipe` test asserting two `exec/output` notifications arrive in order before the result.
  **Host→guest dial:** added the missing piece — the dial target is **not** the friendly id
  `"vm0"` but the compute system's **RuntimeId GUID** (confirmed against hcsshim
  `internal/uvm/create.go`, which caches `properties.RuntimeID`). So our `computecore.dll`
  bindings gained **`HcsGetComputeSystemProperties`** (+ a result-doc-returning wait helper) and a
  new `Driver.RuntimeID`. `internal/vm.Manager.DialGuest` (Windows; stub elsewhere) dials with
  go-winio's root `winio` package — `winio.Dial(ctx, &winio.HvsockAddr{VMID: runtimeID GUID,
  ServiceID: winio.VsockServiceID(5000)})` — caching the GUID on the instance and using a bounded
  `HvsockDialer{Retries,RetryWait}` to absorb the `startVM`→guestd-bind race. The compute-system
  doc now also sets **`DefaultConnectSecurityDescriptor`** (host *connects*, so the bind SD alone
  isn't enough). **Broker `exec`** runs the gate (`door:"compute"`) → `DialGuest` → `CallStream`,
  relaying the guest's `exec/output` straight back to the Hop-2 caller via the per-connection
  notifier from ctx; the guest connection is **per-call** (opened/closed around exec — chosen over
  a persistent pooled client because `rpc.Client` is single-in-flight; pooling/multiplexing is a
  later optimization). **Protocol:** `tools/protogen` gained array (`T[]`) and map (`map<K,V>`)
  types; `protocol.json` gained the `exec` method + `ExecParams`/`ExecResult`, regenerated
  (generated Go/TS now carry `Args []string` / `Env map[string]string`). **`vmctl exec`**:
  `vmctl exec -id vm0 [-cwd …] [-env K=V] -- <cmd> <args…>` streams stdout/stderr live and exits
  with the guest's exit code (`flag` stops at `--`). `go build`/`vet`/`test` green (incl. the new
  `CallStream` test) on Windows and `GOOS=linux`. **Empirical (elevated, reusing the S2.1
  bundle):** `vmctl exec -id vm0 -- ls -la /` streamed the guest root; `cat /etc/os-release` →
  **`Ubuntu 22.04.5 LTS`** (the live "it's really our rootfs" proof deferred since S1.2);
  `python3 --version` → `Python 3.10.12`; exit-code propagation (`sh -c 'exit 3'` → host
  `$LASTEXITCODE == 3`); stdout/stderr split (`… 1>out.txt` captured only `OUT`, `ERR` to the
  console); `-cwd /etc` → `/etc`; `-env GREETING=hello` → `hello`; unknown VM (`-id ghost`) →
  clean error, no hang. Spike runner `.spike/boot_ours.ps1` extended with the exec round-trip;
  binaries `.spike/bin/{host,vmctl}.exe` rebuilt.

---

## Phase 2 — The doors (M3–M4)

> Status: `☑ S3.1` `☑ S4.1`
>
> Now slices are genuinely **feature-vertical**: each door (§10) is one capability,
> independently demoable through `vmctl`. **Files** and **Compute** are unlocked here;
> **Network** is the egress jail.

### S3.1 — M3: Files door (workspace 9p share + jail)
- **Goal:** a host folder appears in the guest at `/workspace`, with the **path jail
  enforced at the privileged boundary** (§8, §10).
- **Work:** add a **Plan9/9p** share to the compute-system doc (host side); `init.sh`
  already mounts 9p — match the tag. Implement broker `readFile`/`writeFile`: canonicalize
  every path against the workspace root, **reject `..` and escaping symlinks**; writes route
  through the policy gate (`ask`) + audit. (Copy Cowork's "broker mediates file I/O" rule.)
- **Touches:** `internal/hcs`/`internal/vm` (9p share), `internal/broker` (`readFile`/
  `writeFile`, jail), `image/guest/init.sh`, `protocol.json` (params already present).
- **Verify:** file written on host shows in guest `/workspace` and vice-versa; a path with
  `..` is **denied**; every access lands in the audit log.
- **Exit:** **Files door** open and contained.
- **Depends:** S2.2.  **Risk:** 9p "bad address" fights on Windows (§15 bug threads);
  symlink canonicalization correctness.
- **Result (2026-05-20): DONE — the Files door opens, contained, and is swappable at
  runtime with no reboot.** The 9p mount mechanism (the slice's central unknown), confirmed
  against hcsshim's guest `plan9.Mount`: HCS serves a Plan9 share over **hvsock**, so the guest
  **dials AF_VSOCK to the host (CID 2) on the share's port (564)**, takes the connection **fd**,
  and mounts `9p -o trans=fd,rfdno=N,wfdno=N,msize=65536,version=9p2000.L,aname=workspace`. A
  shell can't hand an fd to `mount(2)`, so the mount lives in **guestd** (raw `unix.Socket`/
  `Connect`/`Mount`); the scaffolded `init.sh` `trans=virtio` line was wrong and was removed.
  **Pivoted from boot-time to runtime attach** (design call with the user): baking the share into
  the create doc would force a VM reboot to swap workspaces — a non-starter for the planned
  one-VM/many-tabs UI. So the boot doc now carries only an **empty `Plan9: {}` controller**, and
  shares are added/removed on the **running** VM. Our `computecore.dll` bindings gained
  **`HcsModifyComputeSystem`** + `Driver.Modify`; `MakePlan9AddRequest`/`RemoveRequest` author the
  `ModifySettingRequest` (`ResourcePath VirtualMachine/Devices/Plan9/Shares`, `RequestType
  Add`/`Remove`) — **host-side Settings only, no GuestRequest** (we run no GCS; guestd mounts
  itself). New broker verbs **`attachWorkspace`/`detachWorkspace`** (gate `door:"files"`, audited)
  orchestrate both halves: host `GrantVMAccess`+`Modify` then a guestd `mount`/`unmount` RPC over
  Hop 3; `attachWorkspace` auto-detaches any current workspace first, so swapping needs no reboot.
  **`readFile`/`writeFile`** are real (replacing the gated stubs): host-side, broker-mediated I/O
  jailed to the **currently-attached** workspace — `jailPath` rejects absolute paths, `..`
  escapes, and escaping symlinks (resolves the deepest existing ancestor so not-yet-created files
  are still vetted); content is **base64** on the wire so Excel/binary survive (the S2.2 lesson).
  `vmctl` gained `attachWorkspace`/`detachWorkspace`/`readFile`/`writeFile`. Protocol grew the two
  workspace verbs + `AttachWorkspaceParams` (regenerated). `go build`/`vet`/`test` green on Windows
  and `GOOS=linux` (incl. new `jailPath` + round-trip unit tests). **Empirical (elevated):** on a
  running VM, `attachWorkspace ws` → guest `/workspace` shows the host file; broker `writeFile` →
  guest `cat` sees it; guest write → broker `readFile` sees it; `readFile ../../..` is denied;
  **swap** `attachWorkspace ws2` flips `/workspace` to the second folder's file **with no reboot**;
  `detachWorkspace` unmounts it (guest `/workspace` empties, `readFile` → "files door not
  configured"). Serial log shows guestd `mounted share … port 564` / `unmounted share`; every op
  audited `door=files`; no warnings/errors/panics. The VM-worker account's directory ACL via
  `HcsGrantVmAccess` sufficed both directions. Multi-share-per-VM / per-session `sessionId` +
  in-VM bwrap isolation (the multi-tab end-state) remain a later slice; the runtime-attach
  primitives built here are its foundation. Spike runner `.spike/boot_ours.ps1` extended with the
  attach→round-trip→swap→detach flow; binaries `.spike/bin/{host,vmctl}.exe` rebuilt; rootfs
  rebuilt to ship the RPC-mount guestd.

### S4.1 — M4: Network door (egress jail)
- **Goal:** the guest reaches **only** allowlisted destinations; everything else blocked
  (§10 Network, §8 Hop 3).
- **Work:** **start simple** — restricted NIC + allowlist forward proxy (Go), guest traffic
  forced through it. Implement `internal/netjail`. (Later: swap to no-NIC user-mode network
  via `containers/gvisor-tap-vsock` for Cowork-exact isolation — tracked as a follow-up,
  not this slice.)
- **Touches:** `internal/netjail`, `internal/hcs`/`internal/vm` (NIC/HNS config),
  `internal/broker` (egress policy + audit).
- **Verify:** from the guest, an allowlisted host succeeds and a non-allowlisted host
  **fails**; `pip install` works only via the proxy; blocks are audited.
- **Exit:** **Network door** is a jail. The compute door now has no unaudited escape.
- **Depends:** S2.2.  **Risk:** HNS setup; getting the default-deny right (fail-closed).
- **Decision (2026-05-20): went straight to the Cowork-exact end state, NOT the "restricted
  NIC + proxy" start-simple.** Rationale: the guest already has **no NIC** (control RPC + 9p both
  ride hvsock), so a real Hyper-V NIC + HNS + firewall would be *more* Windows machinery, a worse
  fit, and thrown away later. Web research confirmed the model: **Cowork enforces egress as a
  domain allowlist + DNS restriction** (Pluto Security's reverse-engineering) and the **canonical
  gVisor pattern** is to allow/deny in the TCP **forwarder handler** (`r.ID()` → complete or RST).
- **Result (2026-05-20): DONE — the Network door is a default-deny egress jail, verified live.**
  The guest gets a `tap0` from **`containers/gvisor-tap-vsock`** (v0.8.9): its `gvforwarder` (built
  from the lib's `cmd/vm`, shipped in the rootfs, supervised by guestd) dials AF_VSOCK CID 2 :
  **1024** and bridges the tap to the host's user-mode TCP/IP stack served over an **AF_HYPERV
  listener** — the host *is* the guest's whole network (DHCP/DNS/forward). The egress jail is
  **composed, not forked**: `internal/netjail` reuses the library's exported DHCP/DNS/tap/stack but
  supplies the two security seams the lib has no hook for — (1) a **jailed TCP forwarder** that
  dials a destination only if its IP was **pinned** by an allowlisted DNS lookup (closes the
  direct-IP escape), and (2) a **pinning DNS resolver** via `dns.NewWithUpstreamResolver` that
  resolves **only allowlisted names** (NXDOMAIN otherwise, and refuses CNAME/MX/NS/SRV/TXT to
  shrink the DNS-tunnel exfil surface) and records their IPs. No general UDP egress; ICMP not
  forwarded. Policy is a **runtime, default-deny `Allowlist`** the broker owns and the stack
  consults live — `setEgressPolicy {allow:[...]}` swaps it with **no reboot** (the S3.1
  runtime-attach discipline); every resolve/connect decision is **audited `door=network`**. The
  approach was research-led (per the user): web research confirmed the canonical gVisor
  forwarder-handler decision point and Cowork's domain-allowlist + DNS-restriction model.
  - **The central unknown — our own AF_HYPERV listener accepting the guest's gvisor link — was the
    one real fight.** With a plain host listener the guest's connect to CID 2 : 1024 just timed out;
    a global `GuestCommunicationServices` registry entry did **not** fix it. The fix that worked is
    the **per-VM `HvSocket.ServiceTable`** in the compute doc (`internal/hcs/doc.go`): listing our
    egress service GUID (`00000400-facb-11e6-bd58-64006a7986d3`, from the link port) with permissive
    bind/connect SDs + `AllowWildcardBinds` — the same per-VM mechanism HCS uses for its own
    services (e.g. 9p/564, which likewise has **no** registry entry). Confirmed by A/B: registry-only
    failed, ServiceTable made it work, then deleting the registry key and re-running ServiceTable-only
    still worked (so the registry path was dropped entirely — no global host mutation).
  - **Live demo (elevated, `.spike/boot_ours.ps1`):** `tap0` took DHCP lease **192.168.127.2/24**
    with default route via `.1`; default-deny blocked `curl https://example.com`
    (`door=network decision=deny op=resolve`); `setEgressPolicy pypi.org,files.pythonhosted.org` →
    **`pip install requests` succeeded** (downloaded + installed requests + deps), audited
    `resolve allowed pypi.org` / `connect allowed 151.101.x`; a **non-allowlisted host stayed
    blocked** and a **direct-IP `curl https://1.1.1.1` → connection refused** (IP not pinned,
    `connect denied`); clearing the policy denied again **with no reboot**. S2.2 exec + S3.1 Files
    (round-trip + `..` jail) still pass — no regression. `go build`/`vet`/`test` green on Windows
    **and** `GOOS=linux`; rootfs rebuilt to ship `gvforwarder` + `isc-dhcp-client` + tun.
  - **Known v1 limitations (later hardening):** IP-pinning allows any name sharing a pinned CDN IP —
    a MITM/CONNECT proxy matching the exact hostname is the next layer (Cowork's layer 2); the in-VM
    `socket()`-blocking sandbox (bwrap/seccomp, Cowork's layer 1) is a separate slice; multi-VM needs
    a per-VM VMID-bound listener (today the host listens `GUIDWildcard`, fine for one VM).

---

## Phase 3 — The agent (M5)

> Status: `☑ S5a.1` `☑ S5b.1`
>
> Wire the **SDK's seams**, don't write a loop (§8). The same module runs in both topologies.

### S5a.1 — M5a: Agent loop on the HOST (Topology A)
- **Goal:** first end-to-end agent on the real sandbox — brain outside, hands inside.
- **Work:** `packages/agent` hosts `@anthropic-ai/claude-agent-sdk`. Wire seams:
  `executeTool` → broker `exec`/file methods (Hop 2 → guest Hop 3); `callModel` →
  `packages/provider` seam (Anthropic API now, Eliza-shaped later, §13); approvals → broker
  policy gate. Standalone Node CLI, **not** welded to Electron (so S5b reuses it verbatim).
- **Touches:** `packages/agent/*`, `packages/provider/*`, `protocol.json` (tool/approval verbs).
- **Verify:** a task — *"read `/workspace/orders.csv`, compute totals in Python, write
  `summary.csv`"* — completes via the SDK loop, with the file write gated by an approval.
- **Exit:** working agent against a real VM, from a host CLI.
- **Depends:** S3.1 (files), S4.1 (egress for any MCP/network).  **Risk:** seam wiring;
  provider auth/keys; approval round-trip latency.
- **Result (2026-05-20):** ✅ Done. `packages/agent` (standalone npm pkg, `tsx`) hosts
  `@anthropic-ai/claude-agent-sdk` and supplies the three seams: **executeTool** = an in-process
  MCP server (`shell`/`read_file`/`write_file`) over a new TS Hop-2 client (`src/broker/client.ts`:
  named pipe + Content-Length + JSON-RPC 2.0, base64 exec/file framing matching `vmctl`);
  **callModel** = `packages/provider` (model `claude-sonnet-4-6` + `ANTHROPIC_API_KEY` from env,
  `ANTHROPIC_BASE_URL` reserved for Eliza); **approvals** = a pre-baked policy via the SDK's
  `canUseTool` (no end-user prompt — enterprise-shaped, audited). Live run against `vm0`: agent did
  read_file → shell python → write_file and produced `/workspace/summary.csv` (grand total 37.50),
  each call audited; a write to `/etc/...` was **denied** by policy. Notes: the broker Files door is
  workspace-*relative* (the tool translates guest `/workspace/...` paths); `canUseTool` must be the
  chokepoint (an `allowedTools` allowlist bypasses it); pinned `zod@^4` (SDK peer) and
  `@types/node@^22` (Node-22 floor). No `protocol.json` change — reused `exec`/`readFile`/`writeFile`;
  server-authoritative approvals (`checkPolicy` RPC) wait for a real Ask/Deny gate.

### S5b.1 — M5b: Move the loop INTO the guest (Topology B, Cowork parity)
- **Goal:** same module runs as a Node CLI **in the rootfs**; its LLM/MCP/approval calls
  tunnel out over hvsocket to the host broker. Brain + hands in the cage; host holds the keys.
- **Work:** ship Node + the agent module in the rootfs (manifest already lists `node`);
  reverse the `callModel`/MCP/approval transports to go **guest → host broker** over Hop 3.
  Same code, two seams differ (§8 Topology A/B).
- **Touches:** `packages/agent` (transport seam), `cmd/guestd` (host-bound RPC), rootfs manifest.
- **Verify:** the S5a.1 task passes again, loop now **inside** the VM; pull the host process
  and confirm the agent can't reach the model except through the broker.
- **Exit:** Cowork-parity containment.
- **Depends:** S5a.1.  **Risk:** in-guest Node packaging; tunneling MCP cleanly.
- **Result (2026-05-21):** ✅ Done. The same loop now runs **inside** the cage. Because the loop is
  in the sandbox, the two seams flipped *toward simplicity*, not toward tunnels: **executeTool went
  local** — instead of the broker MCP server, the in-guest agent uses the **SDK's built-in coding
  tools** (Bash/Read/Write/Edit/Glob/Grep/…) acting directly on the guest fs (`src/cli-guest.ts`,
  no broker client, no `mcpServers`); **approvals** stayed the pre-baked `Policy` via `canUseTool`,
  extended with a `mode:"guest"` map that allows the in-cage coding set (audited) and denies
  out-of-cage tools (`WebFetch`/`WebSearch`) + unknowns (`src/seams/policy.ts`). **callModel** escapes
  via the **existing egress jail** (S4.1): `vmctl agent` calls `setEgressPolicy(["api.anthropic.com"])`
  then execs the agent over the broker — so **no new guestd/broker/protocol code was needed**.
  Packaging: the rootfs now ships **NodeSource Node 22** (apt's is v12) and the agent + `node_modules`
  baked in at `/opt/atelier/packages/agent` (`image/rootfs/Dockerfile` + a staged build context in
  `image/build.sh`); runs via `tsx`. Live run against `vm0`: `node v22.22.2`, agent did built-in
  Read → Write and produced `/workspace/summary.csv` (grand total **37.50**, identical on the host via
  9p), the write **audited** by policy; exit 0. **Containment proof:** clearing the allowlist
  (`setEgressPolicy []`) makes the model unreachable from the cage (`curl api.anthropic.com` →
  *Could not resolve host*), and the egress jail lives in the broker process, so killing the broker
  kills all guest network. **Deviation from the slice's stated plan:** rather than tunnelling
  `callModel` guest→host so the *host holds the keys*, S5b.1 took the simpler **egress-allowlist**
  path (user decision) — the `ANTHROPIC_API_KEY` rides into the guest process env. The host-side model
  proxy (key never in the guest) is deferred to a future hardening slice; the network *path* is still
  broker-mediated, but key residency is weaker than full Cowork parity.

---

## Phase 4 — The product (M6)

> Status: `◐ S6.1 (implemented + static-verified; live E2E pending)` `☐ S6.2`
>
> Only now does the Electron shell become the top of the stack (§6 — Electron is *last*).

### S6.1 — M6: Electron shell over the broker
- **Goal:** the chat-forward UI (§11) driving the real agent/sandbox.
- **Work:** `apps/desktop/src/main/host-client` — a Hop-2 JSON-RPC **client** to the Go
  broker over the named pipe; expand the IPC seam (typed, allowlisted); chat stream with
  **tool-call cards**, **diff viewer**, **inline approval prompts** (the broker's gate), a
  `/workspace` file panel (§11 build list).
- **Touches:** `apps/desktop/src/main/host-client/*`, `src/main/ipc/*`, `src/preload/*`,
  `src/renderer/features/*`.
- **Verify:** in the running app (real window via `npm start`), a chat task reads a file,
  runs python, and surfaces an approval the user accepts — all on the real VM.
- **Exit:** the product works end-to-end through the UI.
- **Depends:** S5a.1 (at minimum).  **Risk:** streaming UX; approval modal wiring.
- **Result (2026-05-21):** ◐ Implemented and statically verified (Go build/vet/test; agent typecheck +
  tests; desktop typecheck + tests + lint); the in-guest `--serve` loop was **runtime-smoke-verified** on the
  host (real API: multi-turn + hibernate-style export + `--resume` recall). **Live E2E in the UI is the one
  remaining gate** (needs the elevated broker + bundle + a vm0 boot with the rootfs rebuilt to ship the new
  `cli-guest.ts`). Three design points reshaped the slice from its original text:
  - **No interactive approval (enterprise-fixed, user-proof, policy-guided).** The "inline approval prompts"
    became **display-only policy-decision cards**: allowed actions run + audit (quiet badge); denied actions
    don't run, **warn the user**, and are logged. No override (`features/chat/PolicyDecisionCard.tsx`,
    `seams/policy.ts`).
  - **One shared VM, many concurrent persistent sessions (not VM-per-session).** Each WORK session gets its
    own folder mounted at `/sessions/<id>` and its **own long-lived in-guest agent loop**; new composer
    messages feed the SAME running loop (SDK streaming input). Required **two backend extensions**:
    (1) **concurrent multi-share 9p mounts** in one VM (broker `mounts` map + per-session vsock port pool;
    `hcs`/`vm`/`vsock`/`guestd` parameterized by tag/port), and (2) a **host→guest input channel**
    (`execInput`: guestd stdin registry + broker route) to push turns into a running loop.
  - **Hibernate/resume to bound memory.** A host-owned state machine caps live loops: an idle timer + a
    max-active LRU **hibernate** a session (export its context → durable store → kill loop → detach mount);
    selecting a dormant session **resumes** it (re-mount + `query({resume})`). Built on the SDK's
    `session_id` + `resume` (verified). `apps/desktop/src/main/sessions/{manager,store}.ts`.
  - **Touched:** `packages/protocol` (+`sessionId`/`execInput`, optional-field codegen), `services/internal/
    {broker,vm,hcs,vsock}`, `services/cmd/{guestd,vmctl}`, `packages/agent/src/cli-guest.ts`,
    `apps/desktop/src/main/{host-client,sessions,workspace,ipc}/*`, `src/preload`, `src/renderer/*`.
  - **Deferred:** chat-mode wiring (stays mock); user-turn persistence in the rebuilt transcript;
    surviving a vm0 reboot for *live* sessions; per-session OS isolation inside the shared VM.

### S6.2 — M6: Ship
- **Goal:** install like Cowork — no per-run UAC (§9).
- **Work:** install the Go broker as a **LocalSystem Windows service**; restrict the named
  pipe by a **security group** (Docker/Cowork model); MSIX packaging via Electron Forge
  `maker-msix`; skills registry (DXT/`.mcpb` analog, §12) as a follow-on.
- **Touches:** service install, pipe ACL, `apps/desktop/forge.config.ts`.
- **Verify:** clean-box install boots the VM with neither UAC nor Hyper-V-Admin membership
  for the end user.
- **Exit:** installable build.
- **Depends:** S6.1.  **Risk:** service hardening; MSIX signing; pipe ACL correctness.

---

## Critical path (one line)

`S0.1 → S0a → S1.1 → S1.2 → S2.1 → S2.2` is the spine. Everything user-facing hangs off
**S2.2** (host drives guest). S1.3 (matched kernel) and the doors (S3.1/S4.1) can land in
parallel once the spine is up. **Start at S0.1, then the S0a spike.**
