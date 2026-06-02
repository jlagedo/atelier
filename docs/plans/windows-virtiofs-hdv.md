# Windows host↔guest sharing via virtio-fs over HDV

| Field | Detail |
|---|---|
| Status | Design + decision record. Started 2026-06-01. Empirical proof landed (PASS); architecture decided (Option 1); packaging decided (in-broker C-ABI DLL). Implementation not started. |
| Primary reader | Engineers building the Windows file-sharing path for the Atelier cage on a Rocky/EL10 (or any RHEL-family) guest. |
| Companion | [`../research/rocky-el10-migration.md`](../research/rocky-el10-migration.md) — holds the EL10 9p-absence finding and the virtio-fs proof transcript (§1, §1c). This doc holds the *solution* design and the decisions. |
| Decision (architecture) | **Option 1** — keep the Go broker as the HCS VM owner; add a virtio-fs device via the **HDV API**, reusing **OpenVMM's** `virtiofs` device. OpenVMM is a *device*, not a replacement VMM. |
| Decision (packaging) | A **standalone, Atelier-agnostic C-ABI DLL** ("virtiofsd for Hyper-V"), loaded **in-process** by the broker via `syscall.NewLazyDLL` (no cgo), with `catch_unwind` panic isolation. |

---

## 1. Problem

The Atelier cage is moving its guest rootfs from Ubuntu 24.04 to Rocky Linux EL10 (see the
companion doc for the why). That move breaks the Windows file-sharing path:

- **Stock RHEL-family kernels ship no 9p.** Confirmed on Rocky 10.1: `# CONFIG_NET_9P is not set`,
  zero `9p*` modules on disk (companion §1). Atelier's Windows/HCS share rides **9p over hvsock
  (`trans=fd`)** today (`image/guest/init.sh`, `cmd/runner/mount_linux.go:94`), so it dies on EL10.
- **The HCS JSON schema has no virtio-fs device.** Its only directory-share devices are `Plan9`
  (Linux-gated) and `VirtualSmb` (Windows-gated, unusable by a Linux guest). So you cannot simply
  swap 9p→virtio-fs in the compute-system document (companion §1b).

The macOS path is unaffected: VZ (`Virtualization.framework`) exposes a native
`VZVirtioFileSystemDevice`, so macOS already shares via virtio-fs (`driver_darwin.go`).

**The escape hatch:** virtio-fs *is* reachable on Hyper-V — not as a schema device, but through the
documented **HDV (Host Device Virtualization)** API, the same mechanism WSL2 uses (traced live;
companion §1b/§1c). And a stock EL10 guest already ships the in-tree `virtiofs.ko`/`fuse.ko`. The
only missing piece is the **host-side virtio-fs device**, which HDV lets an application supply.

---

## 2. The proof (2026-06-01) — PASS

Before designing anything, the load-bearing question was settled empirically: *can a stock EL10
kernel mount a virtio-fs share supplied by user-space code on a Windows hypervisor at all?*

**Yes.** A prebuilt `openvmm.exe` (OpenVMM CI artifact `x64-windows-openvmm`) booted the **stock
Rocky 10.1 kernel** (`6.12.0-211.16.1.el10_2.0.1.x86_64`, distro bzImage straight from the
`rockylinux:10` `kernel-core` RPM) on the **WHP** backend, with a host folder attached as
`--virtio-fs`. The in-tree `virtiofs.ko`/`fuse.ko` loaded, the device enumerated, and the guest
mounted it **read-write**:

```
loader::linux: detected bzImage format, loading via Linux boot protocol   # no ELF vmlinux needed
Hypervisor detected: Microsoft Hyper-V
virtio-pci 0000:01:00.0: enabling device ... virtio0/device:0x001a        # 0x1a (26) = virtio-fs
modprobe virtiofs rc=0
================ MOUNT_OK ================
hello from the Windows host via OpenVMM virtio-fs                          # guest READ host file
WRITE_OK (wrote /mnt/ws/FROM_GUEST.txt)                                    # guest WROTE back to host
================ PROOF_COMPLETE_PASS ================
```

The full transcript, gotchas, and reproduction live in the companion doc (§1c "Proof-before-build")
and the appendix below.

**Two gotchas this surfaced, carried into the design:**

1. **`pci=off` by default.** OpenVMM prepends `pci=off` to the guest cmdline and only enumerates
   virtio devices when an explicit `--pcie-root-complex` + `--pcie-root-port` is wired and the device
   is attached with `pcie_port=…`. A bare `--virtio-fs "tag,path"` silently fails
   (`virtio-fs: tag <ws> not found`). The HDV device must surface on a (V)PCI bus the guest scans.
2. **No DAX.** `virtio_fs_setup_dax: No cache capability` — functional virtio-fs, no shared-memory
   fast path. Acceptable for a correctness-first cage.

**What the proof did *not* cover:** it ran OpenVMM as a *standalone VMM* on WHP. It did **not** drive
OpenVMM's virtio-fs device **under HCS via HDV** attached to an Atelier-owned compute system. That
HDV attach handshake is the one remaining feasibility unknown (§7).

---

## 3. Layering primer (read this before the options)

A recurring confusion worth nailing: **OpenVMM is not a layer *above* VZ/HCS. It is a peer — an
alternative VMM — that plugs in *below* them, onto the raw hypervisor.**

```
 Windows                                  macOS (Apple Silicon)
 ───────                                  ─────────────────────
   guest VM                                  guest VM
      ▲                                          ▲
 ┌─ VMM layer ──────────────┐            ┌─ VMM layer ──────────────────────┐
 │  HCS/vmwp.exe   OpenVMM   │            │  VZ (Virtualization.fwk)  OpenVMM │
 │  (Microsoft's)  (alt.)    │            │  (Apple's)               (alt.)   │
 └────┬───────────────┬──────┘            └────┬───────────────────────┬─────┘
      │           WHP │  ◄ low-level         hvf │  ◄ low-level (Hypervisor.framework)
      ▼               ▼     hyperv API          ▼                       ▼
 ══ Microsoft Hyper-V hypervisor ══       ══ Apple hypervisor (xnu / EL2) ══
 ══ hardware (VT-x / AMD-V) ═══════       ══ hardware (Apple Silicon) ══════
```

- **HCS** (`computecore.dll`) is a *control plane* + Microsoft's VMM. Calling it spins up
  `vmwp.exe`, the actual VMM. Atelier's "HCS path" = let Microsoft's VMM run the VM, orchestrate via
  HCS.
- **VZ** is Apple's high-level VMM (device models incl. virtio-fs) built on the low-level
  `Hypervisor.framework` (hvf).
- **OpenVMM** is its own VMM. On Windows it drives **WHP** directly (confirmed in the proof:
  `virt_whp::…`), bypassing HCS. On macOS-arm64 it has an **hvf backend** (`vmm_core/virt_hvf`,
  enabled by default in the `openvmm` binary) — so OpenVMM *is* macOS-compatible on Apple Silicon,
  arm64 guests only. It is a *peer* of VZ, not a consumer of it.

Consequences used below:
- **Replacing** HCS/VZ with OpenVMM (Option 3) is swapping the VMM slot — large surgery on a working
  substrate.
- **HDV** (Option 1) is the seam that lets HCS keep owning/running the VM while a *device* — backed
  by OpenVMM's device code — plugs into that HCS VM as an emulated PCI device. OpenVMM's device runs
  *beside* HCS, not in place of it.

---

## 4. Options considered

### Option 1 — Go/HCS broker + OpenVMM virtio-fs device via HDV  ✅ CHOSEN

Keep the broker as the HCS VM owner. Add a virtio-fs device through the HDV API
(`HdvInitializeDeviceHost` → `HdvCreateDeviceInstance` → guest-memory apertures + doorbells), with
the device's FUSE/file logic reused from OpenVMM's `virtiofs` crate.

- **Pros:** preserves the entire proven HCS substrate (lifecycle, hvsocket/vsock, netjail, VBS-capable
  containment, in-box support); new risk isolated to one device; matches WSL's own pattern; reuses a
  mature virtio-fs implementation.
- **Cons:** the reusable thing from OpenVMM is the *device protocol*, **not** the HDV host — see the
  correction below. So there's a real (bounded) Rust component to write. The HDV-attach-to-an-HCS-VM
  handshake is unverified.

**Correction (verified against the cloned `microsoft/openvmm` tree):** the public OpenVMM repo does
**not** ship the HDV device-host bridge. `HdvInitializeDeviceHost`/aperture/doorbell glue appears
only in their `petri` test harness, not as a Rust crate. The `hyper-v\hdv\src\*.rs` bridge paths
seen in `wsldevicehost.dll` strings (`virtiofs.rs`/`api.rs`; see Correction³ in Appendix B) are from
Microsoft's **internal/WSL** tree. What *is* public: the
`virtiofs` device crate, the standalone VMM, and a VPCI/vmbus relay stack. So Option 1 = **write the
HDV transport bridge ourselves**, reusing OpenVMM's `virtiofs` + `virtio` crates for everything above
it. The `virtiofs` crate is also not a clean library (depends on `virtio`, `guestmem`, `vm_resource`,
`pal_async`, `fuse`, `lx/lxutil`) — embedding it means vendoring a subtree of OpenVMM behind a shim.

**Correction² (2026-06-02 — the bridge is an *adapter*, not a rewrite).** A working assumption from
the reconnaissance above — *"OpenVMM's `VirtioPciDevice` is chipset-coupled / `pub(crate)`, so the
HDV transport must reimplement the virtio-pci modern config-space state machine from scratch"* — is
**wrong**, and the deeper `wsldevicehost.dll` forensics (Appendix B) plus the cloned tree disprove
it. `virtio::transport::pci::VirtioPciDevice` is **`pub`** (`pub use pci::VirtioPciDevice` at
`vm/devices/virtio/virtio/src/transport/mod.rs:54`), and `VirtioPciDevice::new`
(`…/transport/pci.rs:148`) takes exactly the seams HDV provides — `GuestMemory`,
`PciInterruptModel`, `Option<Arc<dyn DoorbellRegistration>>`, `&mut dyn RegisterMmioIntercept` — all
public and externally implementable. Microsoft's own closed `hyper-v\hdv\src\virtiofs.rs` is an
*external consumer* of this same public crate (its panic strings reference
`oss\…\virtio\…\transport\pci.rs` and `oss\…\pci_core\…\cfg_space_emu.rs`). So the HDV transport is
**~4 trait adapters over HDV (~350–550 LOC) + a `VirtioPciDevice::new` call**, not a reimplemented
state machine. This shrinks §7 unknown #2 to near-zero and resizes the build.

### Option 2 — implement virtio-fs in pure Go  ❌

Feasible but worst leverage. You'd reimplement *both* the HDV bridge (cgo — fine) **and** the entire
virtio-fs/FUSE/virtqueue device with **zero reuse**: there is **no mature Go package for the device
side** (`hanwen/go-fuse`, `bazil.org/fuse` are host-kernel FUSE servers talking to `/dev/fuse` — the
*wrong end of the wire*, not a guest-facing virtio-fs device). Net: virtiofsd-in-Go, guest-memory
mapped, in a GC language. Only justified under a hard "no Rust" mandate.

### Option 3 — adopt OpenVMM as the Windows VMM (drop HCS)  ❌ (not now)

Standalone OpenVMM gives virtio-fs free over PCIe (exactly what we proved) — no HDV bridge at all.
And because OpenVMM has both WHP and hvf backends, it *could* in principle unify Windows + macOS-arm64
on one VMM.

- **But the cost is replacing both working substrates** — HCS *and* the VZ path on macOS (green on
  `e2e:host` today) — re-driving lifecycle/vsock/netjail against OpenVMM on each. On macOS you'd trade
  Apple's well-supported `Virtualization.framework` for OpenVMM's lower-level, less-battle-tested
  `hvf` backend. On Windows, HCS→OpenVMM is arguably a containment-posture step down (user-mode VMM vs.
  the VBS-capable in-box substrate). A big, separate bet — not warranted off one proof.

**Decision: Option 1.** It contributes a device, not a VMM; isolates the new risk; reuses the hard
part; keeps the cage story intact.

---

## 5. Option 1 architecture

### 5.1 The seam is already platform-neutral

The pleasant surprise: the guest and the VMM driver interface are already shaped for this.

- `mountShare()` (`cmd/runner/mount_linux.go:29`) selects the transport **at runtime**:
  `mountVirtiofsShare` if the kernel has virtio-fs, else `mount9pShare`. The broker passes the same
  `{port, tag, target}` either way.
- `mountVirtiofsShare` (`:65`) already handles both shapes Atelier uses — legacy single `/workspace`
  and per-session `/sessions/<tag>` — mirroring VZ's `SingleDirectoryShare`/`MultipleDirectoryShare`.
- The VMM driver seam is already neutral: `AttachWorkspace`/`DetachWorkspace`/`WorkspaceShare`.
  - macOS (`driver_darwin.go:84`, `:459`) holds a live `fsdev *vz.VirtioFileSystemDevice` tagged
    `vsock.WorkspaceShareTag`; attach/detach rebuild the directory map and call `fsdev.SetShare(...)`
    on the running device.
  - Windows (`driver_windows.go:204`) does `hcs.MakePlan9AddRequest(...) → ModifyComputeSystem`.

So Option 1's job is concentrated: **make `windowsDriver.AttachWorkspace/DetachWorkspace` drive a
live HDV virtio-fs device's share map — the byte-for-byte analog of `darwinDriver` calling
`fsdev.SetShare` — instead of Plan9 add/remove.** Everything above the seam (doors, Session Manager,
guest mount) is unchanged. Windows converges onto the same guest mount path as macOS; the 9p stack
retires.

### 5.2 Components

```
        Windows host                                    guest (Rocky EL10)
 ┌──────────────────────────────┐
 │ atelierd (Go broker, owns VM)│  HCS compute system ─────►  vmwp/HCS runs the VM
 │  • windowsDriver             │                                   │ VPCI bus
 │      Attach/DetachWorkspace  │                                   ▼
 │      └─ hvfs_set_shares() ───┐                            virtio-fs PCI device (0x1a)
 │  • loads hyperv_virtiofs.dll │ │  C ABI, in-proc                 │  virtiofs.ko binds
 │    (syscall.NewLazyDLL)      │ │  (no cgo)                       ▼
 └──────────────────────────────┘ │                          mount -t virtiofs <tag>
   the DLL's own threads ◄═════════┘   FUSE over virtqueue      /workspace  |  /sessions/<tag>
   serve FUSE ═══ guest memory (HDV apertures + doorbells/irqs) ═══►
        │ serves
        ▼
   E:\…\sessions\<id>\   (real host folders the broker maps)
```

| Component | New? | Responsibility |
|---|---|---|
| `windowsDriver` (`driver_windows.go`) | reworked | Attach/Detach build `{tag→path,ro}` and call the DLL's `hvfs_set_shares`; drop Plan9 calls. `WorkspaceShare.Port` becomes a Windows no-op. |
| HCS compute-system doc (`internal/hcs`) | changed | declare the `FlexibleIov`/VPCI slot so the guest gets the device; retire `MakePlan9Add/RemoveRequest` on Windows. |
| **`hyperv_virtiofs.dll`** | **new (Rust)** | the device host: `hdv` bindings + OpenVMM `virtio` transport over HDV + OpenVMM `virtiofs` device + the share map + the directory jail. C-ABI surface (§6). |
| Build (`scripts/build-all.mjs`) | changed | fetch/build the DLL (pinned), stage next to `atelierd.exe` (Windows only). |
| Guest (`cmd/runner/mount_linux.go`) | none | already does virtiofs. |
| Guest (`image/guest/init.sh`) | minor, later | drop the now-dead `9pnet_*` modprobes from the Windows bundle. |

### 5.3 Runtime data path

Guest reads `/workspace/foo` → `virtiofs.ko` puts a FUSE READ on the virtqueue → doorbell → HDV
wakes the DLL's worker → it reads descriptors from guest memory via an aperture, does the NTFS read,
writes the FUSE reply into guest buffers, raises the completion interrupt. **The file I/O path is
DLL-thread ↔ guest directly over guest memory — it does not pass through the broker's logic.** The
broker only ever pushes `set_shares` deltas (rare, control-plane). This is why in-proc vs.
out-of-proc is irrelevant to file throughput (§6.2).

### 5.4 Per-session shares (mirror VZ)

One virtio-fs device, one config-time tag (`WorkspaceShareTag`), with a mutable directory map:
- legacy single workspace → `SingleDirectoryShare` semantics (dir at device root; guest mounts at
  `/workspace`);
- sessions → `MultipleDirectoryShare` semantics (each session a named subdir; guest mounts once at
  `/sessions` and subdirs appear/vanish as the broker adds/removes them).

The broker maintains the `{tag→hostPath}` map and pushes it live via `hvfs_set_shares`, exactly as
`darwinDriver` rebuilds and `SetShare`s it.

### 5.5 Security

Today the path jail lives host-side in `broker/files.go` (per call: rejects `..`, blocks symlink
escape, path-relative). With virtio-fs the guest gets **direct FS authority over whatever root the
device serves**, so the jail moves **into the DLL's FUSE server** and must replicate those
guarantees: confine every resolution beneath the served root (the Windows `RESOLVE_BENEATH` analog —
reparse-point/junction aware), honor read-only, validate tags. This is a generic correctness property
of *any* virtio-fs daemon (not Atelier policy) and is the security-critical core of the component.

Trust placement is settled in §6 (the DLL runs in-broker by deliberate decision); the residual
mitigations there (`catch_unwind`, watchdog) bound a serving fault to an error return rather than a
broker abort.

---

## 6. Packaging decision — standalone agnostic DLL, loaded in-broker

### 6.1 The DLL is a standalone, Atelier-agnostic product

The Rust component is designed as **"virtiofsd for Hyper-V"**: a standalone library that attaches an
OpenVMM virtio-fs device to *any* HCS/Hyper-V guest via HDV. It contains **no** Atelier concepts — no
`WorkspaceShare`, no sessions, no broker. Its vocabulary is purely virtio-fs/HDV: *compute systems,
tags, directory maps, read-only*. Atelier is one consumer of its C ABI; it could be shipped and
open-sourced by itself (the missing open counterpart to WSL's closed `wsldevicehost.dll`).

**Internal layering** (lower layers reusable for any HDV device, not just virtio-fs):

| Crate | Responsibility |
|---|---|
| `hdv-sys` | raw FFI to the HDV C API (`HdvInitializeDeviceHost`, `HdvCreateDeviceInstance`, `HdvCreateGuestMemoryAperture`, `HdvRegisterDoorbell`, callbacks). |
| `hdv` | safe RAII wrapper (device host, instance, aperture, doorbell, PCI config). |
| `virtio-hdv` | OpenVMM virtio transport **over** `hdv` (guest mem ← apertures, kick ← doorbells, config space). The open `wsldevicehost` slice. |
| `hyperv_virtiofs` (cdylib) | wires OpenVMM's `virtiofs` onto `virtio-hdv`; exposes the C ABI. |

**The C ABI (the whole public contract):**

```c
// hyperv_virtiofs.h — stable, versioned. Host-agnostic: works in-proc or in a stub.
typedef struct hvfs_device hvfs_device;

// Attach a virtio-fs device to an EXTERNALLY-owned HCS system, by id.
// Non-blocking; serving runs on the DLL's own threads.
int32_t hvfs_attach(const char* hcs_system_id,
                    const char* device_json,     // {"tag":"ws", ...}
                    hvfs_device** out);

int32_t hvfs_set_shares(hvfs_device*, const char* shares_json);  // mirrors VZ SetShare:
                                                                 // {"ws":{"path":..,"ro":..},...}
int32_t hvfs_detach(hvfs_device*);

typedef void (*hvfs_log_fn)(int level, const char* msg, void* ctx);
void    hvfs_set_logger(hvfs_log_fn, void* ctx);
```

Go binds this with `syscall.NewLazyDLL`/`NewProc` — **the same mechanism Atelier already uses for
`computecore.dll`** — so the DLL introduces **no cgo** into the Windows broker build. Lifecycle is
direct calls: `hvfs_attach` / `hvfs_set_shares` / `hvfs_detach`. A thin provided `.exe` wrapper
(`main → load dll → run`) can ship alongside for standalone testing.

### 6.2 In-process vs. separate process — DECIDED: in-process

This was debated. **Decision: load the DLL in the broker process.** Rationale, including the points
that overturned an initial lean toward a separate sandboxed process:

- **Consistency with the existing TCB (decisive).** The broker *already* runs `gvisor-tap-vsock` —
  a full userspace TCP/IP stack parsing attacker-influenced **guest packets** — plus the vsock RPC
  server, in-process. That is a *larger, more complex* host-side, guest-facing attack surface than a
  virtio-fs device, and it is in Go. Demanding a separate sandboxed process for an OpenVMM-derived
  virtio-fs device **in Rust** (a smaller, more auditable surface) while keeping the netstack in-proc
  would be incoherent. Same trust class; the safer one doesn't deserve stricter treatment.
- **Rust memory safety.** The device is `unsafe` only at the guest-memory/virtqueue boundary (small,
  auditable), and reuses OpenVMM's implementation. Memory-corruption probability is low enough not to
  justify a process boundary on its own.
- **Privilege drop is largely illusory.** `HdvInitializeDeviceHost` maps guest physical memory and
  almost certainly requires a privileged caller, so the device host must be privileged regardless. A
  child of the elevated Windows service would also inherit elevation by default. Little to gain.
- **No data-path cost.** File I/O is DLL-thread ↔ guest over guest memory; it never traverses the
  broker. The process boundary would sit only on the rare `set_shares` control path. So separate-proc
  buys ~nothing on throughput.
- **Operational simplicity.** One process, direct calls, no child supervision / control pipe /
  handshake.

**The one residual handled in-proc:** Rust panics are process-fatal across an FFI boundary
(`panic=abort`). Mitigation, baked into the DLL contract from day one: build `panic=unwind` and wrap
**every FFI entry point and every device-host worker-thread body** in `std::panic::catch_unwind`, so
a serving panic returns an error / drops the request instead of aborting `atelierd`; add a watchdog so
a wedged device can be torn down and re-attached. This recovers ~all of the crash isolation a separate
process would have provided.

*When the separate-process posture would be revisited:* if a future spike shows HDV runs happily from
a de-privileged/`ExternalRestricted` host "for free" (HCS launches it), or if the device host ever
grows untrusted-input surface beyond the audited unsafe boundary. Not worth engineering up front.

---

## 7. Open unknowns → spike plan

Resolved so far: a stock EL10 guest mounts an OpenVMM virtio-fs device read-write on WHP (§2), **and
now over our own HDV bridge** (milestone 2 below).

> **Milestone 2 — DONE (2026-06-02).** A stock Rocky Linux 10 guest under Hyper-V/HCS **mounts a host
> directory over our HDV virtio-fs bridge** — reads `SENTINEL.txt`, writes `FROM_GUEST.txt` back —
> with no 9p (`hyperv-virtiofs/crates/hcs-testvm/tests/attach_virtiofs.rs`, `PROOF_COMPLETE_PASS`).
> `virtio-hdv` implements `hdv::pci::PciOps` over OpenVMM's public `VirtioPciDevice`/`VirtioFsDevice`,
> backing guest-memory DMA with `HdvCreateGuestMemoryAperture`, MSI with `HdvDeliverGuestInterrupt`,
> and PCI config + BAR MMIO with HDV's device-vtable callbacks. Three findings shaped it: (a) the
> VMBus VID owns guest-facing BAR placement and delivers MMIO pre-decoded as `(bar, offset)` — the
> guest never touches our config BAR registers, so we route via *internal* BAR bases; (b) the copy
> APIs (`HdvReadGuestMemory`) lack DMA rights → apertures are mandatory (matching WSL); (c) HDV
> apertures are an **evictable cache**, not a coherent mapping (WSL's `HdvGuestMemoryEvictionWorker`),
> so a persistent mapping + interrupt re-arm + boot retry mask a residual staleness window. Follow-ups:
> wire `hvfs_attach`, the eviction protocol for full coherency, DAX, `set_shares`.

> **Milestone 2b — `hvfs_attach` wired through the C ABI (2026-06-02).** The proven mount now runs
> through the **shipped front door**: `hvfs_attach(hcs_system_id, device_json, &out)` opens the
> externally-owned compute system (`HcsOpenComputeSystem`), proxy-registers an HDV device host
> (`DeviceHostSupport` → `DeviceHost::from_proxy`), and calls `VirtioHdvDevice::attach`; a guest mounts
> the share and prints `PROOF_COMPLETE_PASS` driven entirely by the exported C symbols
> (`hyperv-virtiofs/crates/hcs-testvm/tests/attach_abi.rs`). The ABI header is unchanged (no version
> bump). `device_json` carries the initial share + guest RAM:
> `{ "tag", "path", "ro", "memory_mb" }`. **Two deliberate deferrals**, both documented in the repo
> README roadmap: (a) **caller-supplied device GUIDs** — host/class/instance are fixed well-known
> constants the consumer must mirror in its `FlexibleIov` slot, rather than overridable via
> `device_json`; (b) **live `set_shares`** — still `HVFS_ERR_NOT_IMPLEMENTED`, since OpenVMM's
> `VirtioFsDevice` binds its share at construction (the initial share comes in via `device_json`).

> **`set_shares` design probe — device-hotplug spike (2026-06-02).** Atelier maps a host dir
> **per session, live**, on the one running VM (the VZ backend does this via `VZMultipleDirectoryShare`
> + `fsdev.SetShare()`). Two ways to match it on Windows: **(A)** a composite multi-root FUSE wrapper
> (one device, mutable `/sessions/<tag>` router — OpenVMM's `VirtioFs` is single-root + immutable, so
> we'd fork/own it, the hard part being inode-namespace translation); or **(B)** hot-plug a virtio-fs
> **device per share** over VPCI — the OpenVMM-endorsed answer
> ([microsoft/openvmm#861](https://github.com/microsoft/openvmm/issues/861): "device per tag … add/remove
> a device after start … straightforward on the VPCI transport, Windows-only"), reusing `VirtioFsDevice`
> unchanged and **merging** the caller-supplied-GUIDs item. B's risk is *runtime* device add/remove
> through our HDV proxy; a staged go/no-go spike (`hyperv-virtiofs/docs/hotplug-spike.md`) tested it.
>
> **Spike verdict (2026-06-02): B is viable — go.** Hot-**add** of concurrent virtio-fs devices works
> (Stage 1 ✅, Stage 2 ✅): one shared HDV device host (`Arc<DeviceHost>`), N devices, each the
> **well-known `VIRTIO_FS_DEVICE_ID`** as class id + a unique instance GUID — exactly WSL's model
> (a *custom* class id rejects the second device with `ERROR_HV_INVALID_PARAMETER`; **not**
> one-host-per-device — two device hosts fail with `0xC0370030`). Hot-**remove** is the one constraint:
> `FlexibleIov` Remove returns `ERROR_NOT_SUPPORTED` on Win11 26200, independent of `SchemaVersion`
> ({2,7} tested) — a platform gap WSL shares and works around. **Teardown = reclaim-at-recycle**: drop
> host-side device refs when a session ends and rely on VM hibernate/restart to actually free them
> (atelier already recycles). So `set_shares` = device-per-share hot-add + a per-VM share/device
> registry, capped to bound lingering devices between recycles.

Remaining, in priority order:

1. **HDV attach handshake (the linchpin). ✅ RETIRED (2026-06-02).** The EL10 guest now **enumerates an
   HDV-attached device end-to-end**, proving Option 1. Final spike
   `hyperv-virtiofs/hcs-testvm/tests/attach_proxy.rs`: create the VM with a `FlexibleIov` slot →
   `HdvInitializeDeviceHostForProxy` + our `IVmDeviceHostSupport` → `HdvProxyDeviceHost` (register_hr
   `S_OK`) → `HdvCreateDeviceInstance` → `start` succeeds (no `0x8000FFFF`); the VID drives the device
   `Initialize → GetDetails → Start → ReadConfigSpace`, and the guest logs
   `hv_pci <instanceId>: PCI host bridge to bus 0001:00` / `pci 0001:00:00.0: [1af4:1100]` over VMBus
   VPCI. The path the in-process `HdvInitializeDeviceHost` *couldn't* take (it failed FlexibleIov's
   `FinishReservingResources`) is the **proxy** path; the missing call was `HdvProxyDeviceHost`. Guest
   needs `hv_vmbus` + `pci-hyperv` loaded. Full ABI + handshake: `hyperv-virtiofs/docs/hdv-proxy-abi.md`
   and Appendix C — **the reverse-engineered proxy ABI is confirmed against Microsoft's public
   `VmDeviceHost.pdb`** (on the symbol server; `wsldevicehost.pdb` is not): `HDV::CreateDeviceHostForProxy(
   IVmDeviceHostSupport const&, _GUID const&, HDV_DEVICE_HOST_FLAGS)` etc. match our inferences exactly. Everything else is engineering (swap the driverless device for `VirtioFsDevice`,
   task #8).

   <details><summary>Earlier (superseded) finding</summary>

   **Host→HDV half RESOLVED (2026-06-02 attach spike).** Against a compute system we own,
   `HdvInitializeDeviceHost` **+** `HdvCreateDeviceInstance(PCI device, vtable)` both **succeed
   in-process** — but that path then fails FlexibleIov's `FinishReservingResources`; the working path
   is the proxy registration above.
   </details>
2. **OpenVMM transport pluggability.** ~~Whether the `virtio` crate's transport seam cleanly accepts
   an HDV-backed `GuestMemory` + external queue-notify, or assumes OpenVMM's own PCI/VPCI
   transport.~~ **Resolved (2026-06-02, Correction² in §4 + Appendix B):** `VirtioPciDevice` is `pub`
   and `::new` consumes a custom `GuestMemory` (via the public `GuestMemoryAccess` trait), a
   `DoorbellRegistration`, a `PciInterruptModel::Msix(&MsiTarget)`, and a `RegisterMmioIntercept` —
   all public/externally implementable. `virtio-hdv` is a small adapter, not a fight.
3. **VPCI slot declaration — the active blocker (sharpened 2026-06-02 attach spike).** The minimal
   `LinuxKernelDirect` HCS VM has **no PCI bus at all**: the guest kernel logs `PCI: Fatal: No config
   space access function found` and `PCI: System does not support PCI`. So even though the HDV attach
   succeeds (#1), an HDV-created PCI device has nowhere to enumerate. The compute-system document
   must declare a **VPCI bus** — and most likely a `FlexibleIov` device slot bound to an
   emulator/instance id (as WSL does via `ModifyComputeSystem` Add; companion §1c) — so the guest
   gains PCI config-space access and a bus the HDV device surfaces on. This mirrors the
   OpenVMM-standalone gotcha (it needed an explicit `--pcie-root-complex` + `--pcie-root-port`; §2).
   **Result (2026-06-02, second attach spike).** Added a `FlexibleIov` slot to `build_document()`
   (map-key = device instance GUID = HDV `DeviceInstanceId`; `EmulatorId` = HDV `DeviceClassId`;
   `HostingModel: ExternalRestricted`). Strong partial progress — the slot mechanism and GUID
   coupling **work**: HCS recognized the slot, matched both GUIDs to our in-process emulator, and
   invoked it. But `HcsStartComputeSystem` then **fails**: *"Microsoft Flexible IO Device: Failed to
   finish reserving resources … 0x8000FFFF … error … from emulator when invoking API 'Initialize'.
   Emulator ID A7E1…0001 ('Unknown')"*. Callback tracing shows our HDV-vtable `Initialize` **is
   reached and returns `S_OK`**, yet HCS's FlexibleIov VID throws `E_UNEXPECTED` in
   `FinishReservingResources` **before** ever calling `GetDetails`. `HdvInitializeDeviceHostEx` +
   `InitializeComSecurity` made no difference (so it is not COM-security).

   **Working interpretation — an architecture mismatch, not a bug.** `FlexibleIov` +
   `HostingModel: ExternalRestricted` expects an **out-of-process, HCS-registered** emulator (cf. the
   `IVmExternalRestrictedFlexIOVDevice` RTTI symbol in `vmdevicehost.dll`, companion §1c) — i.e.
   WSL's model — not our **in-process** `HdvInitializeDeviceHost` device host. So the two paths we
   proved separately (in-process attach works in isolation; FlexibleIov gives the bus + routes by
   GUID) do **not** simply compose.

   **A-vs-B experiment — out-of-process alone is NOT the missing piece (2026-06-02, third attach
   spike, `hcs-testvm/tests/attach_oop.rs` + `src/bin/attach_child.rs`).** To split the fork we moved
   the emulator into a **separate process the test spawns and owns**: the parent creates the VM (with
   the slot) but opens *no* device host; a child binary opens its own `HCS_SYSTEM` via
   `HcsOpenComputeSystem` and runs `HdvInitializeDeviceHost` + `HdvCreateDeviceInstance` there, then
   signals ready so the parent starts only after the device exists. Result: the child's device host
   registers and HDV **invokes our `Initialize` in the child** (so cross-process registration partly
   works) — yet `HcsStartComputeSystem` fails **byte-for-byte identically** to in-process
   (`0x8000FFFF`, `FinishReservingResources`, emulator `A7E1…0001` **`'Unknown'`**). **The failure is
   invariant to the process boundary.** So the variable was never "who spawns the process" — atelierd
   spawning a helper does *not* help. The missing piece is the **emulator registration/identity
   contract**: HCS's FlexibleIov VID resolves the `EmulatorId` to an emulator it considers `'Unknown'`
   because an ad-hoc `HdvInitializeDeviceHost` host — in *any* process — is not a registered
   `ExternalRestricted` emulator. This **rules out fork option (a)** as a mere process/packaging tweak
   and points squarely at **(b): HCS must own/recognise the emulator**, launched or registered through
   the channel the `EmulatorId` names (how `wslservice`/`wsldevicehost` actually wire up — the next
   forensic target). Tracked as task #16. (§6.2 in-process packaging decision is now likely forced
   out-of-process — which is *better* for containment: the guest-memory-touching code leaves the
   privileged broker.)

   **RESOLVED mechanism (2026-06-02, WSL OSS source — Appendix C).** `microsoft/WSL` is open; its
   host side spells out the real protocol. The `EmulatorId` is **not** the COM CLSID and there is **no**
   static registry mapping. The slot is **hot-added** (`HcsModifyComputeSystem`) *after* the device host
   is registered with the partition by **`HdvProxyDeviceHost(HCS_SYSTEM, IVmDeviceHost-IUnknown, pid,
   &ipcSection)`** — the call our spike never made (which is why the VID saw `'Unknown'`). The COM
   surrogate/CLSID is WSL's sandboxing, not a requirement. Concrete next build: bind
   `HcsModifyComputeSystem` + `HdvProxyDeviceHost` + `HdvInitializeDeviceHostForProxy`, implement an
   `IVmDeviceHost`, host it in-process first. See Appendix C for the full step-by-step + GUIDs.
4. **Windows directory jail.** Confirm the `RESOLVE_BENEATH`-equivalent (reparse/junction-safe path
   confinement) for the FUSE server.

Then the normal path: `virtio-hdv` adapter → `hyperv_virtiofs` DLL + C ABI → `windowsDriver` client →
`build-all` staging → `e2e:host` on Windows.

---

## 8. What stays the same

Broker remains the HCS owner (this is Option 1, **not** Option 3 — OpenVMM is a device, not the VMM).
The Hop-2 control plane over hvsocket, `exec`, the netjail/egress jail, the protocol doors
(`attachWorkspace`/`detachWorkspace` unchanged at the wire level — only their Windows backend swaps
Plan9 → HDV virtio-fs), the Session Manager, and the entire macOS/VZ path are untouched.

---

## 9. Appendix — reproducing the proof (§2)

Artifacts (throwaway, outside the repo): `E:\dev\spike\` and `E:\dev\openvmm-bin\openvmm.exe`
(OpenVMM CI artifact `x64-windows-openvmm`, downloaded via `gh run download`).

**Build the EL10 direct-boot artifact** (`E:\dev\spike\build-rocky-initramfs.sh`, run under WSL):
pulls `rockylinux:10`, `dnf install kernel-core kernel-modules kernel-modules-core util-linux kmod`,
copies out `/lib/modules/<ver>/vmlinuz` (bzImage) and packs the whole Rocky rootfs as an
`initramfs.cpio.gz` whose only addition is a self-testing `/init` (mount `-t virtiofs ws /mnt/ws`,
read a sentinel, write one back, print `PROOF_COMPLETE_PASS/FAIL`).

**Boot under OpenVMM** (`E:\dev\spike\run-spike.ps1`), the load-bearing flags:

```
openvmm.exe -k vmlinuz -r initramfs.cpio.gz \
  --pcie-root-complex rc0,segment=0,start_bus=0,end_bus=255,low_mmio=4M,high_mmio=1G \
  --pcie-root-port rc0:fs \
  --virtio-fs pcie_port=fs:ws,E:\dev\spike\share \
  --com1 console -c "console=ttyS0" -m 2G -p 2 --hv
```

The `--pcie-root-complex`/`--pcie-root-port` + `pcie_port=fs:` wiring is mandatory (the `pci=off`
gotcha, §2). The runner captures the serial console, watches for the sentinel, and tears the VM down.

---

## 10. Sources

- Proof transcript + EL10 9p-absence: companion [`rocky-el10-migration.md`](../research/rocky-el10-migration.md) §1, §1c.
- Microsoft Learn — HDV API (`virtualization/api/hcs/reference/hdv/devicevirtualization`:
  `HdvInitializeDeviceHost`, `HdvCreateDeviceInstance`, `HdvCreateGuestMemoryAperture`,
  `HdvRegisterDoorbell`).
- `microsoft/openvmm` (MIT): `vm/devices/virtio/virtiofs`, the WHP backend (`virt_whp`), the macOS
  hvf backend (`vmm_core/virt_hvf`), CLI (`openvmm.dev/guide`). Verified absent from the public tree:
  the HDV device-host bridge (only in `petri` test glue).
- Live forensics (this machine, 2026-06-01): WSL `FlexibleIov`/`ModifyComputeSystem` event,
  `vmdevicehost.dll`/`wsldevicehost.dll` exports + strings — companion §1c.
- Atelier seams: `cmd/runner/mount_linux.go`, `internal/vmm/driver_darwin.go`,
  `internal/vmm/driver_windows.go`, `internal/hcs`, `image/guest/init.sh`.

---

## 11. Appendix B — `wsldevicehost.dll` forensics (2026-06-02)

To bound the one remaining build (the HDV transport bridge) before writing it, we did surface
forensics on the shipped WSL device host — **not** disassembly. `wsldevicehost.dll`
(`C:\Program Files\WSL\`, **1.6 MB**, version **1.2.14.0**, built with `rust 1.94.0-ms`) is a Rust
binary whose embedded panic-location strings expose its full source-file map. Every path carries a
**depot prefix** that cleanly separates open from closed:

- **`oss\…`** → the public `microsoft/openvmm` tree. Confirmed present (we already depend on all of
  these): `oss\vm\devices\virtio\virtiofs\…` (`lib`/`inode`/`section`/`virtio`/`virtio_util`),
  `oss\vm\devices\virtio\virtio\…\transport\pci.rs` (**`VirtioPciDevice`**) + `…\transport\task.rs`
  + `…\queue.rs`, `oss\vm\devices\pci\pci_core\…\cfg_space_emu.rs` + `…\capabilities\msix.rs` +
  `…\msi.rs`, `oss\vm\vmcore\guestmem\…`, `oss\vm\vmcore\…\line_interrupt.rs`,
  `oss\vm\devices\support\fs\{fuse,lxutil}\…`, plus `mesh`/`pal_async`/`task_control` support.
- **`hyper-v\…`** → Microsoft's **internal** Windows depot — **not** mirrored to the public repo.

The closed bridge is therefore just two internal crates:

| Closed file (`hyper-v\…`) | Our open counterpart |
|---|---|
| `hdv\src\api.rs` (25 panic-refs — the largest) | `hdv-sys` + `hdv` (HDV FFI + RAII) |
| `hdv\src\virtiofs.rs` | **`virtio-hdv`** (the adapter for virtio-fs — the one file to write) + the `hyperv_virtiofs` cdylib wiring |
| `hdv\src\{virtio_net,virtio_pmem}.rs` | not needed (we only ship virtio-fs) |
| `hdv\src\util.rs` | shared helpers |
| `wsldevicehost\src\{lib,hdv,virtiofs,virtio_net,…}.rs` | not needed (WSL's COM/DLL `ExternalRestricted` shim) |

> **Correction³ (2026-06-02, full Ghidra decompile).** The two rows above are revised from the
> earlier surface-strings inference. A complete Ghidra decompilation of the DLL
> (`wsldevicehost.dll.c`, 295k lines) shows the bridge has **no single `virtio_hdv.rs`** — the
> adapter is split **one file per device type** (`virtiofs.rs`, `virtio_net.rs`, `virtio_pmem.rs`)
> over a shared `api.rs` + `util.rs`. So `virtio-hdv` maps to `virtiofs.rs` (plus its slice of
> `api.rs`), not to a monolithic ~1400-line file. The "~1700/~1400/~166 L" sizes were a guess from
> max panic line-refs and don't survive the decompile (panic line numbers are stored as separate
> integer args, not adjacent to the path string, so they aren't recoverable that way) — dropped
> rather than restated. Three device files ⇄ **three distinct `HdvCreateDeviceInstance` vtables** in
> the decompile (`DAT_180132400`, `DAT_180132528`, `DAT_180168cf0` at the three call sites), i.e. WSL
> ships three FlexibleIov emulators; we need only the virtio-fs one.

**Two conclusions that shaped the build:**

1. **The bridge reuses public crates** (its device-file panic strings reference
   `oss\…\virtio\…\transport\pci.rs` and `oss\…\pci_core\…\cfg_space_emu.rs`) — i.e. it drives the
   *public* `VirtioPciDevice` rather than reimplementing it. This is the evidence behind Correction²
   (§4): our `virtio-hdv` is an adapter over public crates, ~350–550 LOC, with Microsoft's
   `hdv\src\virtiofs.rs` (+ its slice of `api.rs`; see Correction³ above) as the structural map. The
   relevant HDV→OpenVMM seam map:
   `HdvCreateGuestMemoryAperture` → `GuestMemoryAccess`/`GuestMemory::new`; `HdvRegisterDoorbell` →
   `DoorbellRegistration`; `HdvDeliverGuestInterrupt` (`msi_address`/`msi_data`, seen in
   `api.rs`) → `PciInterruptModel::Msix(&MsiTarget)`; HDV BAR intercept callbacks →
   `RegisterMmioIntercept`; HDV `Read/WriteConfigSpace` → `VirtioPciDevice`'s config space.
2. **No one has republished the bridge.** A web + Kagi sweep for "wsldevicehost.dll → OpenVMM/OpenHCL
   source" found only the public `oss\` layers (which we already use) and — as the sole repo hit for
   the bridge query — *our own* `jlagedo/hyperv-virtiofs`. So this project is the open counterpart,
   not a duplicate of existing open code.

**Two further findings from the full decompile (2026-06-02) that validate our build:**

3. **The proxy ABI is confirmed from the *closed* caller's side, and it's a clean function seam.**
   The decompiled call (line 37679) is `HdvInitializeDeviceHostForProxy(param_3 /*ctx GUID*/,
   param_4 /*IVmDeviceHostSupport*/, &out)` — our exact 3-arg shape — and `HdvCreateDeviceInstance`
   (line 36185) is `(host, 1 /*Pci*/, classId, instanceId, vtable, ctx, &out)`, matching `hdv-sys`.
   Both call sites are guarded by the **same test-seam pattern**: `if (indirect == 0) { real HDV
   export } else { (*indirect_vtable + 0x28)(same args) }` — i.e. WSL routes the call through an
   optional trait object so it can substitute a mock host in unit tests. That the export is swappable
   behind a vtable is direct evidence the ABI is a self-contained function boundary, which is why our
   single-process spike (`hdv::proxy`, no COM surrogate) works.
4. **The "adapter, not rewrite" thesis is proven by the binary itself.** The embedded panic paths
   show the shipped DLL links these *public* OpenVMM crates verbatim:
   `oss\vm\devices\virtio\virtiofs\src\lib.rs` (the FUSE virtio-fs device we drive),
   `oss\vm\devices\pci\pci_core\src\{cfg_space_emu,capabilities\msix}.rs`, `oss\vm\vmcore\guestmem`,
   `oss\vm\chipset_device`, the `virtio` transport/queue/common, and `lxutil`/`fuse`. That is exactly
   the milestone-2 seam list — present, public, and already in our `Cargo.toml` — so `virtio-hdv` is
   an adapter over these, not a reimplementation.

The raw strings dump (and the full Ghidra decompile, `E:\tmp\wsldevicehost\wsldevicehost.dll.c`) are
archived locally; the decompile is the authoritative source for the points above.

## 12. Appendix C — WSL OSS source: the FlexibleIov / HDV-proxy host protocol (2026-06-02)

`microsoft/WSL` is **open source** (`github.com/microsoft/WSL`, cloned to `E:\dev\WSL`). It contains the
**host/orchestration** side (what `wslservice` does) in clear C++ — though **not** the device-host
emulator itself (the `HdvInitializeDeviceHostForProxy` + `HdvCreateDeviceInstance` + virtiofs caller is
still the closed `wsldevicehost.dll` = OpenVMM `oss\` + the closed `hyper-v\hdv\` bridge of Appendix B;
the OSS grep finds those symbols only in `.def` proxy-stubs). This is the **authoritative** answer to
"how does the `EmulatorId` resolve" and it **corrects** the binary-only guess that "EmulatorId == the
COM CLSID."

**Two distinct GUIDs, two roles** (`WslCoreVm.cpp:2200` `AddGuestDevice(VIRTIO_FS_DEVICE_ID, Admin ?
VIRTIO_FS_ADMIN_CLASS_ID : VIRTIO_FS_CLASS_ID, …)`):
- **`EmulatorId`** written into the `FlexibleIov` doc = a device-**type** GUID = the HDV `DeviceClassId`.
  Virtio-fs: `VIRTIO_FS_DEVICE_ID = {872270E1-A899-4AF6-B454-7193634435AD}` (`GuestDeviceManager.h:14`).
  (`FLEXIO_DEVICE_ID = {a8679153-843f-467f-ad7e-f429328f7568}` is the VID's own category id — and is
  exactly the `"DeviceId"` in our spike's failure JSON.)
- **`ImplementationClsid`** = the device host's **COM class**, `CoCreateInstance`d by the host with
  `CLSCTX_LOCAL_SERVER` (`GuestDeviceManager.cpp:54`) so the registered **AppID `DllSurrogate`** runs it
  out-of-process in `dllhost.exe`. Virtio-fs: `{60285AE6-…}` (`WslDeviceHost_VirtioFs`) / `{7e6ad219-…}`
  (`…_Admin`). **This is WSL's sandboxing choice, not part of the HCS contract.**

So the `EmulatorId`→emulator link is **not** a static registry mapping. It is established at **runtime**
by a proxy protocol. The host side (`wslservice`; `DeviceHostProxy.cpp`, `GuestDeviceManager.cpp`) does:

1. Create the VM — **`FlexibleIov` is *not* in the initial document** (added later, hot, by modify).
2. Get an `IPlan9FileSystem` to the device host (WSL: `CoCreateInstance(ImplementationClsid,
   CLSCTX_LOCAL_SERVER)` → dllhost surrogate; **we could host it in-process instead**).
3. `server->AddSharePath(name, hostPath, flags)` — configure the share (host folder).
4. `server->CreateVirtioDevice(vmId, deviceHostSupport, tag, &instanceId)` — the **device host** then
   (closed side) calls `HdvInitializeDeviceHostForProxy` + `HdvCreateDeviceInstance(DeviceClassId =
   EmulatorId, DeviceInstanceId = instanceId, vtable)`, and calls back
   `deviceHostSupport->RegisterDeviceHost(itsIVmDeviceHost, GetCurrentProcessId(), &ipcSection)`.
5. Host `RegisterDeviceHost` (`DeviceHostProxy.cpp:147`) dynamically loads **`HdvProxyDeviceHost`** from
   `vmdevicehost.dll` and calls `HdvProxyDeviceHost(HCS_SYSTEM, IVmDeviceHost-as-IUnknown,
   TargetProcessId, &IpcSectionHandle)` — **this** is what registers the (out-of-process) device host
   with the partition (signature confirmed, `wdk.h:409`). It also puts the host process in a
   kill-on-close job.
6. Host `ModifyComputeSystem` **Add** `VirtualMachine/Devices/FlexibleIov/<instanceId>` with
   `{EmulatorId = VIRTIO_FS_DEVICE_ID, HostingModel = ExternalRestricted}` (`DeviceHostProxy.cpp:61-67`).
   The VID's `FinishReservingResources` now resolves the slot to the device host registered in step 5
   (via `IVmDeviceHost::GetDeviceInstance(DeviceClassId, DeviceInstanceId)`) → the guest enumerates it.

Doorbells/MMIO take a side channel: the device host reaches the **VM worker process** via
`GetVmWorkerProcess(vmId, …)` (`vmwpctrl.dll`) → `IVmVirtualDeviceAccess::GetDevice(FLEXIO_DEVICE_ID,
instanceId)` → `IVmFiovGuestMemoryFastNotification` / `IVmFiovGuestMmioMappings`.

**The COM contract** (`src/windows/service/inc/windowsdefs.idl`):
`IVmDeviceHost {78523d62-…}` `GetDeviceInstance(classId, instanceId, IUnknown**)` (device-host side);
`IVmDeviceHostSupport {e31aa49b-…}` `RegisterDeviceHost(IVmDeviceHost*, pid, UINT64* ipcSection)` (host
side); `IPlan9FileSystem {7649D52D-…}` `AddSharePath`/`CreateVirtioDevice`/…; `IPlan9FileSystemHost
{8434F839-…}` doorbell callbacks; `Plan9FileSystem` coclass `{AFC7B6DE-…}`.

**Why our spike failed (root cause, corrected):** we declared a `FlexibleIov` slot but **never
registered a device host via `HdvProxyDeviceHost`**. The in-process `HdvInitializeDeviceHost` registers
on a path the `ExternalRestricted` VID does not consult, so `FinishReservingResources` finds no emulator
for the `EmulatorId` → `'Unknown'` → `0x8000FFFF`. Process boundary was never the variable (the
out-of-process spike, `attach_oop.rs`, failed identically) — the **missing call is `HdvProxyDeviceHost`**
(plus an `IVmDeviceHost` to hand it, and `HcsModifyComputeSystem` to hot-add the slot).

**Implication for our build (task #16).** The mandatory contract is `HdvInitializeDeviceHostForProxy`
(device side) + `HdvProxyDeviceHost` (host side) + `HcsModifyComputeSystem` (hot-add the slot) +
an `IVmDeviceHost` COM object. The COM-surrogate / registry-CLSID / AppID machinery is **optional** —
WSL uses it to sandbox the device host, but `HdvProxyDeviceHost` takes an `IVmDeviceHost` pointer + a
`ProcessId`, so atelierd can host the device host **in-process** and pass its own PID for the minimal
proof. (For containment we may still *want* a surrogate later — §6.2 — but it isn't required to make the
guest enumerate the device.) To bind next: `HcsModifyComputeSystem` (hcs-sys); `HdvProxyDeviceHost` +
`HdvInitializeDeviceHostForProxy[Ex]` (hdv-sys, dynamic-load from `vmdevicehost.dll`); the `IVmDeviceHost`
vtable. `HdvProxyDeviceHost`'s signature is in WSL `wdk.h`; the `HdvInitializeDeviceHostForProxy[Ex]` ones are
**not** in any header (closed `wsldevicehost`) but were **reverse-engineered** from `vmdevicehost.dll`
and validated against the known exports — full ABI, the device↔host handshake, and the IIDs in
`hyperv-virtiofs/docs/hdv-proxy-abi.md`. Net device-side shape:
`HdvInitializeDeviceHostForProxy(ctx, IVmDeviceHostSupport*, HDV_HOST* out)` — it `QueryInterface`s the
support callback (IID `e31aa49b-…`), builds the host, and drives
`IVmDeviceHostSupport::RegisterDeviceHost` → `HdvProxyDeviceHost(system, IVmDeviceHost, pid, &ipc)`. All
three exports + `HcsModifyComputeSystem` are now bound in `hdv-sys`/`hcs-sys`. The remaining build is
two small Rust COM objects (`IVmDeviceHost`, `IVmDeviceHostSupport`) + the in-process spike; only `ctx`
(arg1) and `GetDeviceInstance`'s expected return are unverified, to be settled when the spike runs.

**Built + PROVEN (2026-06-02).** Done in one process (`hyperv-virtiofs`): we author **only**
`IVmDeviceHostSupport` (`hdv::proxy::DeviceHostSupport`) — HDV builds the `IVmDeviceHost` inside
`ForProxy` and hands it to us, and serves `GetDeviceInstance` itself. `ctx` resolved to a non-null
`*const GUID` (device-host id; null faulted). The spike (`attach_proxy.rs`) registers via the proxy,
hot-adds/declares the slot, and the **guest enumerates `pci 0001:00:00.0: [1af4:1100]` over VMBus VPCI**
(`register_hr S_OK`, `start` succeeds, no `0x8000FFFF`). Linchpin retired; see §7 #1 +
`hyperv-virtiofs/docs/hdv-proxy-abi.md`.
