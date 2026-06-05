# Migrating the cage to Rocky Linux EL10

| Field | Detail |
|---|---|
| Status | Research / decision record, May 2026; HCS share-device research + HDV/OpenVMM virtio-fs finding (§1c) added 2026-06-01. **virtio-fs proof executed 2026-06-01 — PASS**: a stock Rocky 10.1 kernel mounts an OpenVMM-supplied virtio-fs share read-write on Windows/WHP (§1c "Proof-before-build", spike Step 0b). No Atelier code changed. |
| Primary reader | Engineers evaluating a future utility-VM rootfs move from Ubuntu 24.04 to Rocky Linux EL10. |
| Decision | Target Rocky Linux EL10 for the spike. |
| Solution design | The Windows share rework (virtio-fs over HDV) has its own design + decision record: [`../plans/windows-virtiofs-hdv.md`](../plans/windows-virtiofs-hdv.md). This doc holds the *evidence* (9p absence, the virtio-fs proof); that one holds the *solution*. |
| Main risk | RHEL-family kernels omit 9p (**confirmed on Rocky 10.1, §1**) and the HCS *JSON schema* has **no virtio-fs device** (§1b) — *but* virtio-fs **is** reachable on Hyper-V via the documented **HDV API + OpenVMM** (§1c), now **empirically proven**: a stock EL10 kernel mounts an OpenVMM virtio-fs share read-write on Windows/WHP (§1c Proof). Residual risk is integration, not feasibility: driving that same device under **HCS/HDV** (vs. OpenVMM-standalone) and embedding OpenVMM's virtiofs. Fallbacks if HDV integration stalls: network CIFS/NFS over the egress door, or a custom kernel with 9p. |

## Why this, why now

Atelier's cage currently uses `ubuntu:24.04` (`image/rootfs/Dockerfile:4`). A
regulated or financial deployment may require a RHEL-family base so path-jail,
TOCTOU, driver, and hardening findings reproduce on the target environment.

Target Rocky Linux EL10 as a free, bug-for-bug stand-in for licensed RHEL/UBI.
Prefer Rocky over Alma because Rocky tracks RHEL behavior more tightly. Keep RHEL
UBI plus an external kernel as a later option only if FIPS-validated crypto is
required.

Prefer EL10 over EL9 because EL10 has kernel 6.12 and glibc 2.39, closer to the
current Ubuntu HWE kernel baseline.

## Current state — the Ubuntu pipeline we're porting

The cage image is a four-stage `docker export` pipeline. None of the Dockerfiles run as live
containers; `image/build.sh` exports each into an ext4 disk.

| Stage | File | Role |
|---|---|---|
| **rootfs** | `image/rootfs/Dockerfile` (`FROM ubuntu:24.04`) | cage filesystem **+ the pinned guest kernel** |
| **imager** | `image/imager/Dockerfile` (`FROM ubuntu:22.04`) | just `e2fsprogs`; runs `mke2fs -d` to pour the export into ext4 |
| **agent** | `image/agent/Dockerfile` (`FROM ubuntu:24.04`) | builds artisan `node_modules` + partisan `uv` venv onto the `/opt` runner volume |
| **kernel** | `image/kernel/fetch-kernel.sh` | extracts `vmlinuz` (+ initrd) from the rootfs `/boot` |

The governing rule is the **§7 kernel coupling**: kernel, its `/lib/modules/<ver>`, and the boot
initramfs all come from **one apt transaction** (`linux-image-virtual-hwe-24.04` + `initramfs-tools`,
`image/rootfs/Dockerfile:44-45`) so they can't drift; `fetch-kernel.sh` just lifts `/boot/vmlinuz-*`
out of that tree. Boot is validated at runtime by `image/guest/init.sh`, which `modprobe`s the exact
driver set the cage needs and then drops to uid 1001 under bubblewrap.

## What we learned (the research)

### 1. The RHEL family deliberately does not ship 9p — this is the headline

RHEL and its rebuilds (Rocky, Alma) build kernels with **`CONFIG_NET_9P` unset**. Confirmed for
EL9 / Rocky 9: `mount -t 9p` fails with *"9p is unknown file system"*. Red Hat has steered away from
9p toward **virtiofs** for years (virtio-9p "not optimized for virtualization"; QEMU deprecated the
9p proxy backend). The only way to get 9p on RHEL is the **elrepo** mainline kernel — which, per the
Rocky forum, means *"it is no longer a true RHEL environment,"* defeating the whole reason to pick
Rocky (parity with the bank's RHEL).

**Confirmed for EL10 too (2026-06-01).** Inspected the `rockylinux/rockylinux:10` image (**Rocky Linux
10.1 "Red Quartz"**): after `dnf install kernel-core kernel-modules-core kernel-modules
kernel-modules-extra`, the shipped kernel config carries **`# CONFIG_NET_9P is not set`** and **zero**
`9p*` module files exist on disk. So `9pnet`, `9pnet_virtio`, the trans_fd transport, and the
`9p`/v9fs filesystem (`CONFIG_9P_FS` depends on `CONFIG_NET_9P`) are **all absent** — not in base, not
in `kernel-modules-extra`. The EL9 result reproduces on EL10; the working assumption is now a verified
finding.

**Why it hits Atelier directly.** `image/guest/init.sh` modprobes the 9p stack (`9pnet_fd`,
`9pnet_virtio`, `9p`), and host↔guest shares (`/workspace`, per-session `/sessions/<tag>`) ride 9p on
Windows. Per the design, **virtiofs is the VZ/macOS path; 9p is the home for WSL2 / client Hyper-V** —
i.e. exactly the **Windows/HCS target** we most want to harden. On a RHEL cage the `9p*` modprobes
simply fail if the modules are absent. **The trap (see §1b): there is no "move to virtiofs" escape on
HCS — HCS exposes no virtio-fs device at all.** So if EL10 ships without 9p, the Windows share has no
in-schema substitute. This is the single biggest risk and the most article-worthy friction: *the
enterprise distro won't mount the share mechanism the enterprise hypervisor pairs with.* (The
hypervisor *does* offer virtio-fs — through a separate API, **HDV**, not a JSON-schema device — see
§1c, which is the likely real fix.)

### 1b. The HCS *JSON schema* has no virtio-fs device — but a separate API does (§1c)

Deep research against primary sources (microsoft/hcsshim, kernel.org, microsoft/WSL PRs) established
what the **declarative HCS schema** offers. The first two facts below stand; the third bullet's
inference was later **overturned by live forensics (§1c)** — virtio-fs *is* reachable on HCS, just not
as a JSON-schema device:

- **The public HCS schema (`computecore.dll`) has exactly two host↔guest directory-share devices:
  `Plan9` and `VirtualSmb`. No virtio-fs, in any verified schema version** (through Win11 21H2 / 2.6;
  no later rev adds one). hcsshim's generated bindings aren't stale here — the capability genuinely
  isn't in the API.
- **Both are OS-locked.** hcsshim gates `AddPlan9` on `operatingSystem != linux` (Plan9 is the *only*
  Linux share path) and `AddVSMB` on `operatingSystem != windows` (VSMB is **Windows-guest-only**).
  And mainline Linux has **no VMBus-SMB redirector** — its SMB client speaks only TCP/IP and RDMA — so
  a Linux guest physically cannot consume an HCS VSMB share even if the gate allowed it.
- **WSL2's virtio-fs is *not* a JSON-schema device — but it IS reachable (correction).** It attaches
  via a `FlexibleIov` device + the **HDV API** through `ModifyComputeSystem`, backed by an OpenVMM
  emulator. The earlier claim here — *"private `wsldevicehost` vdev, not `FlexibleIoDevice`, not
  replicable"* — was **wrong**; see §1c for the live evidence and the resulting (real) Atelier path.
- **Comparable projects, for context:** virtio-fs in Kata Containers runs on **KVM**; Firecracker
  shares no filesystem; Docker Desktop's WSL2 backend uses **9p**. On Hyper-V specifically the
  virtio-fs implementation is **OpenVMM** (what WSL embeds), reachable via HDV (§1c).

**Narrowed kernel requirement.** Atelier's Windows path mounts 9p with **`trans=fd` over hvsock**
(`init.sh:47-50`), not 9p-over-virtio. So the make-or-break EL10 kernel config is **`CONFIG_NET_9P`**
(the `9pnet` core, which contains trans_fd) **+ `CONFIG_9P_FS`** (v9fs) — **not** `CONFIG_NET_9P_VIRTIO`.
The `9pnet_virtio` modprobe in `init.sh:84` is a no-op on the HCS path and can be dropped there.

**Net (corrected by §1c):** EL10 omits `CONFIG_NET_9P` (confirmed Rocky 10.1), so Plan9 is out for a
stock kernel. The fixes, best-first: **(a)** implement virtio-fs via HDV + OpenVMM (§1c) — keeps a
true RHEL kernel *and* a modern share; **(b)** network CIFS/NFS over the egress door; **(c)**
block-device + broker sync; **(d)** custom kernel with 9p built in. See Challenges #1.

### 1c. The escape hatch exists after all — virtio-fs on HCS via the HDV API + OpenVMM (live forensics, 2026-06-01)

§1b's "no virtio-fs anywhere on HCS" was **wrong**, corrected by direct observation on a Windows 11
box. The JSON-schema *device list* (Plan9/VirtualSmb) really is the whole story for a declarative
`createComputeSystem` doc — **but virtio-fs reaches the guest through a different, documented API:
HDV (Host Device Virtualization).** WSL uses it, and the entire chain was traced live.

**The forensic chain (all observed on this machine):**

1. **WSL attaches virtio-fs as a `FlexibleIov` device via `ModifyComputeSystem`.** The HCS operational
   log (`Microsoft-Windows-Hyper-V-Compute-Operational`, event 2007) shows on every WSL boot:

   ```json
   {"RequestType":"Add",
    "ResourcePath":"VirtualMachine/Devices/FlexibleIov/<instance-guid>",
    "Settings":{"EmulatorId":"872270e1-a899-4af6-b454-7193634435ad","HostingModel":"ExternalRestricted"}}
   ```

   Six of them (virtio-fs is device-per-tag; the host folder is **not** in `Settings` — it is set
   out-of-band by the emulator). `EmulatorId 872270e1…` matches the virtio-fs DEVICE_ID in WSL PR
   #13822. **So WSL's virtio-fs IS a `FlexibleIoDevice` — §1b's "adversarially refuted, not
   FlexibleIoDevice" verdict was wrong; live evidence beat inference.**
2. **The host emulator is HDV.** `C:\Windows\System32\vmdevicehost.dll` (the generic external device
   host) exports `HdvRegisterDoorbell` / `HdvRegisterDoorbellPage` and carries the RTTI symbol
   `IVmExternalRestrictedFlexIOVDevice`. `Hdv*` = **Host Device Virtualization**.
3. **WSL's emulator is OpenVMM, in Rust.** `C:\Program Files\WSL\wsldevicehost.dll` contains the
   strings `virtiofs` / `virtio-net` / `virtio-pmem` and source paths `hyper-v\hdv\src\virtio_hdv.rs`
   - `oss\vm\devices\virtio\…\transport\pci.rs` — the **open-source OpenVMM virtio stack**.

**Deeper strings pass (2026-06-02).** The DLL's panic-location strings give its full source map,
split by depot prefix — **`oss\…`** = public `microsoft/openvmm` (what we reuse), **`hyper-v\…`** =
Microsoft's internal Windows depot (closed). The closed glue is just two crates: `hdv` (`api.rs`
~1700 L, `virtio_hdv.rs` ~1400 L, `virtiofs.rs` ~166 L) and a `wsldevicehost` COM/DLL shim. The
bridge's own strings reference `oss\…\transport\pci.rs` + `oss\…\pci_core\…\cfg_space_emu.rs`, i.e.
it **reuses the public `VirtioPciDevice`** rather than reimplementing it — so the open re-build is an
adapter over public crates, not a rewrite. Full table + map in the solution doc
[`../plans/windows-virtiofs-hdv.md` §11 (Appendix B)](../plans/windows-virtiofs-hdv.md).

**Both halves are public:**

- **HDV is a documented Microsoft API.** Microsoft Learn ("Device Virtualization",
  `virtualization/api/hcs/reference/hdv/`): *"The Host Compute APIs allow applications to extend the
  Hyper-V platform with virtualization support for generic PCI devices… not natively supported by the
  Hyper-V platform… The host-side code that virtualizes the device is supplied by the application."*
  `computecore.dll` exports `HdvInitializeDeviceHost`, `HdvCreateDeviceInstance`,
  `HdvCreateGuestMemoryAperture`, `HdvRegisterDoorbell`; callbacks `HDV_PCI_DEVICE_INITIALIZE`,
  `HDV_PCI_DEVICE_SET_CONFIGURATION`, `HDV_PCI_DEVICE_INTERFACE`.
- **OpenVMM open-sources the device.** `microsoft/openvmm` (MIT, Rust) ships a
  `vm/devices/virtio/virtiofs` crate; supports virtio-fs/9p/net/pmem **on a Windows host** over the
  **VPCI transport** (issue #861), CLI `openvmm.exe --virtio-fs "tag,D:\share"`. The source path baked
  into `wsldevicehost.dll` is this repo — **WSL's virtio-fs is OpenVMM's virtio-fs.**

**Why this matters: there IS a true-RHEL-kernel virtio-fs path on Windows.** A stock Rocky 10 guest
already ships `virtiofs.ko` + `fuse.ko` (`CONFIG_VIRTIO_FS=m`, `CONFIG_FUSE_FS=m`, `CONFIG_VIRTIO_PCI=y`;
verified on `rockylinux:10`, 2026-06-01) — only the host device was missing, and HDV supplies it:

```text
broker (owns the HCS VM)
  └─ host-side HDV device host (Rust sidecar, OpenVMM virtiofs crate)
       ├─ HdvInitializeDeviceHost(computeSystem)        [documented]
       ├─ HdvCreateDeviceInstance(virtio-fs PCI device) [documented]
       └─ backs it with the workspace folder
guest Rocky 10:  mount -t virtiofs <tag> /workspace     [in-tree driver]
```

Because Atelier *owns* the compute system, the natural route is **in-process
`HdvInitializeDeviceHost`** (extend your own VM), not WSL's platform-style `ExternalRestricted`.

**Honest hard parts (not a config flip):**

- A **Rust component** enters the substrate (OpenVMM's virtiofs/hdv crates, run as a host-side
  device-host sidecar the Go broker drives). Whether OpenVMM is embeddable as a library vs. shelling
  `openvmm.exe`'s device host is **unverified** — needs a spike.
- **Security surface — the load-bearing caveat for a *containment* product.** An HDV device host has
  **direct guest-memory access** and runs host-side; it lands in the TCB / attack surface. WSL runs it
  deliberately `ExternalRestricted` (sandboxed) — Atelier should match that posture, not host it
  in-broker-process. A design decision, not free.
- **DAX / perf** is limited (**confirmed** in the proof below: `virtio_fs_setup_dax: No cache
  capability` — virtio-fs works, but without the shared-memory DAX fast path). Acceptable for the
  cage's correctness-first goal; revisit if throughput matters.
- **Signing / VBS / HVCI** capabilities of the HDV host process need verifying — normal dev diligence.

**Proof-before-build — DONE, PASS (2026-06-01).** Ran the spike end-to-end on this Windows 11 box,
zero Atelier code. A prebuilt `openvmm.exe` (OpenVMM CI artifact `x64-windows-openvmm`) booted the
**stock Rocky 10.1 kernel** as shipped — `6.12.0-211.16.1.el10_2.0.1.x86_64`, a distro bzImage pulled
straight from the `rockylinux:10` `kernel-core` RPM, wrapped in an initramfs of the unmodified Rocky
rootfs whose only addition was a self-testing `/init`. OpenVMM loaded the bzImage directly
(`loader::linux: detected bzImage format, loading via Linux boot protocol` — no ELF `vmlinux`
conversion needed) on the WHP backend (`Hypervisor detected: Microsoft Hyper-V`). The host folder was
attached as `--virtio-fs pcie_port=fs:ws,E:\dev\spike\share` on a PCIe root complex/port. Result:

```text
virtio-pci 0000:01:00.0: enabling device          # the OpenVMM virtio-fs PCI device enumerated
/sys/bus/pci/devices/0000:01:00.0/virtio0/device:0x001a   # 0x1a (26) = virtio-fs
fuse: init (API version 7.41) ; modprobe virtiofs rc=0    # stock in-tree el10 modules load
virtiofs virtio0: virtio_fs_setup_dax: No cache capability
================ MOUNT_OK ================            # mount -t virtiofs ws /mnt/ws
hello from the Windows host via OpenVMM virtio-fs    # guest READ the host sentinel
WRITE_OK (wrote /mnt/ws/FROM_GUEST.txt)              # guest WROTE back; file appeared on the host
================ PROOF_COMPLETE_PASS ================
```

So **a true RHEL-family kernel mounts an OpenVMM-supplied virtio-fs share, read-write, on Windows.**
The host side (does OpenVMM's virtio-fs device run on WHP) and the guest side (does the stock EL10
driver bind it) are both confirmed in one boot. Two gotchas worth carrying into the broker work:
(1) OpenVMM defaults the guest cmdline to `pci=off` and only enumerates virtio devices when an
explicit `--pcie-root-complex` + `--pcie-root-port` is wired and the device is attached with
`pcie_port=…` — a bare `--virtio-fs "tag,path"` silently has nowhere to appear (first run failed
`virtio-fs: tag <ws> not found`); (2) no DAX (above). Artifacts/scripts: `E:\dev\spike\`
(`build-rocky-initramfs.sh`, `run-spike.ps1`, `init`); binary `E:\dev\openvmm-bin\openvmm.exe`.

What this proof does **not** yet establish (still ahead for the real integration): driving the same
OpenVMM virtio-fs device under **HCS/HDV** (the spike used OpenVMM as a standalone VMM on WHP, not as
an HDV device host attached to an Atelier-owned HCS compute system), and whether OpenVMM's virtiofs is
embeddable as a library vs. shelling its device host. Those are the next unknowns — but the
load-bearing question ("can a stock EL10 guest do virtio-fs on this hypervisor stack at all") is now
**yes, empirically**.

**Sources:** Microsoft Learn HDV API (`virtualization/api/hcs/reference/hdv/devicevirtualization`,
`HdvInitializeDeviceHost`, `HdvCreateDeviceInstance`); `microsoft/openvmm`
(`vm/devices/virtio/virtiofs`, issue #861, openvmm.dev guide); live HCS operational-log capture +
`vmdevicehost.dll` / `wsldevicehost.dll` string analysis (this machine, 2026-06-01).

### 2. Hyper-V / vsock on RHEL is rock-solid — the counterweight

The control plane ports cleanly. RHEL is a **fully certified Hyper-V guest**: Red Hat ships and
certifies the built-in LIS drivers (`hv_vmbus`, `hv_netvsc`, `hv_storvsc`), `hv_sock` has been in-tree
since Linux 4.14, and there's a first-class `hyperv-daemons` package (KVP/VSS/fcopy). So the cage's
vsock RPC backbone (`hv_sock` / `vmw_vsock_virtio_transport`, `init.sh`) is on RHEL's *supported* path
— the opposite of the 9p story. **Net: control plane (vsock) is a non-issue; data plane (file share)
is the work.**

### 3. RHEL "image mode" (bootc) is Atelier's exact pattern, blessed

Atelier's pipeline — *write a Dockerfile, export it into a bootable disk* — is precisely what Red Hat
now ships as **image mode for RHEL (bootc)**: define the OS as a container (`FROM rhel-bootc`), then
`bootc-image-builder` produces a bootable disk (raw / qcow2 / vmdk / ISO). Two details land on us:

- The **minimal bootc base image is "bootc + kernel + dnf"** — a near-perfect description of what the
  cage wants to be (kernel + tiny userspace + agent payload). Not pre-shipped; generated locally via
  multi-stage build — exactly our model.
- RHEL VM images use **`dracut-config-generic`** — a host-agnostic, all-drivers initramfs. This
  resolves the dracut-config question below: install `dracut-config-generic`, get the boring default,
  don't hand-craft host-only.

For the "clone of enterprise boringness" goal this is the aspirational endpoint: the most
enterprise-legitimate cage isn't a hand-rolled `docker export` of Ubuntu — it's a **bootc image
`FROM` a RHEL/Rocky base**, built by `bootc-image-builder`. Bigger rearchitecture, but the genuinely
boring-enterprise target.

### 4. UBI cannot be the cage — but fits one layer in

**UBI ships no kernel** (it borrows the container host's). So UBI can never be the VM cage base alone.
Where it fits Atelier: the **agent payload** on the `/opt` runner volume (`image/agent/Dockerfile`) is
a kernel-less userspace tree, so a UBI base *there* gives "Red Hat all the way down" on the
redistributable part, while the cage kernel comes from full Rocky/RHEL `kernel`.

## The porting surface — by tier

Grounded in a full pass over `image/`. Most lives in `rootfs/Dockerfile` + `build.sh`, plus matching
swaps in imager and agent Dockerfiles. **glibc stays glibc; every kernel-module name we depend on is
identical on EL10** (the question is whether the module is *present*, not renamed).

### Critical (the actual project)

| # | Change | Where | Note |
|---|---|---|---|
| C1 | **Kernel package** `linux-image-virtual-hwe-24.04` → `kernel` (+ `kernel-core`/`kernel-modules-core`) | `rootfs/Dockerfile:45` | No "HWE" line in EL; 6.12 is fixed. Open question: which module split carries `hv_sock`/`virtiofs` — `kernel-modules-core` vs `kernel-modules`. **There is no `kernel-virt` package in stock RHEL/Rocky — do not design around it.** |
| C2 | **Host↔guest share rework** — stock Rocky 10.1 has no 9p (§1), so Plan9 is out | `image/guest/init.sh` (drop 9p modprobes; add `virtiofs`/`fuse` or `cifs`) **+ `services/internal/{vmm,hcs,netjail}`/broker** | Best fix: **virtio-fs via HDV + OpenVMM** (§1c) — keeps a true RHEL kernel and a modern share, reaches the Go broker (new HDV device host). Fallbacks: network CIFS/NFS over the egress door; block-device + broker sync; custom kernel with 9p. **Step 0 of the spike.** |
| C3 | **initramfs: `initramfs-tools` → `dracut`** | `rootfs/Dockerfile:44`; `build.sh:219`; `fetch-kernel.sh` | Output filename changes: `/boot/initrd.img-<ver>` → `/boot/initramfs-<ver>.img`. Update the extraction glob + error text. Use `dracut-config-generic` (finding #3). |

The VZ arm64 gunzip step in `fetch-kernel.sh:35` is distro-independent and stays (EL10 arm64 kernel is
also a gzip'd Image).

### Mechanical (substitution, low risk)

| Change | Where |
|---|---|
| `apt-get install/update` + `rm -rf /var/lib/apt/lists/*` → `dnf install -y` + `dnf clean all`; drop `DEBIAN_FRONTEND`, `--no-install-recommends` | all four Dockerfiles |
| `iproute2` → `iproute` (EL drops the `2`) | `rootfs/Dockerfile:42` |
| NodeSource `deb.nodesource.com/setup_22.x` → `rpm.nodesource.com/setup_22.x` | `rootfs/Dockerfile:52`, `agent/Dockerfile:25` |
| `ripgrep`: not in EL base — needs **EPEL**, or vendor the static `rg`. For an auditable image, lean **vendor** (EPEL widens the trust surface). | `rootfs/Dockerfile:38` |
| `python3-seccomp` → likely `python3-libseccomp` — **verify** | `agent/Dockerfile` |
| base swaps: imager `ubuntu:22.04` → `rockylinux:10`; agent `ubuntu:24.04` → `rockylinux:10` (must match rootfs glibc for venv/node_modules ABI) | `imager/Dockerfile:9`, `agent/Dockerfile:13` |
| rename `UBUNTU_VERSION` → `DISTRO_VERSION` + tags | `build.sh:20,35,242,272` |

### Untouched

All of `image/guest/init.sh` **except the 9p lines** — every other `modprobe`, the tmpfs layout
(`/home/atelier`, `/sessions`, `/workspace`), the `/opt` runner-volume mount, static `tap0`/
`gvforwarder` networking, `modules_disabled` hardening — is distro-agnostic, **provided C1's kernel
carries those modules**. `mke2fs -d`, `qemu-img convert`, `docker export`, CRIT-05 permission
normalization, `useradd -u 1001`, the runner-volume design, and the entire Go host side are
unaffected (except C2's share path).

## Challenges, ranked

1. **The Windows/HCS share path (C2).** The make-or-break. Stock Rocky 10.1 has **no `CONFIG_NET_9P`**
   (§1, confirmed), so Plan9 is out unless you ship a custom kernel. Options, best-first:
   **(a) virtio-fs via the HDV API + OpenVMM (§1c)** — the real fix: keeps a true RHEL kernel *and* a
   modern share; cost is a host-side HDV device-host (Rust sidecar) + its TCB/security review.
   **(b)** network CIFS/NFS — host SMB/NFS server reached by the guest's in-tree `cifs.ko`/nfs over the
   gvisor-tap-vsock door. **(c)** writable scratch VHD synced by the broker's Files door. **(d)** custom
   kernel with 9p built in (forfeits kernel parity). All but (d) reach the Go broker, not just the
   image. **Resolve this first** — start with the §1c `openvmm.exe --virtio-fs` proof.
2. **EL10 kernel module coverage (C1).** Does plain `kernel` + `dracut-config-generic` carry
   `hv_sock`, `virtiofs`, `vmw_vsock_virtio_transport`, `tun`, `loop`, ext4/overlay/fuse? Answerable
   *without booting* via `dnf install kernel dracut` then `find /lib/modules` / `lsinitrd`. If a
   required module isn't in any available `kernel-modules*` package, that itself is a clean finding.
3. **The real gate is a boot.** Per project rule, we don't claim it works until `npm run e2e:host`
   boots a RHEL cage on VZ (macOS dev) and HCS (Windows target) and clears `init.sh`'s driver checks
   with partisan reaching the model through the egress jail. The HCS/Windows boot can't be verified
   from a Mac — say so rather than claim success.
4. **Rootfs size.** EL10's `kernel-modules*` split may be coarser than Ubuntu's lean HWE set —
   measurable, not dangerous.
5. **Supply-chain auditability.** EPEL-for-ripgrep and NodeSource-RPM both widen the trust surface of
   a security-research image. Prefer vendored static binaries where the "boring/auditable" goal beats
   convenience.

## Two endpoints worth naming

- **Pragmatic:** Rocky EL10 rootfs through the *existing* `docker export` pipeline (apt→dnf, dracut,
  `kernel`). Smallest *image* diff — but the share is the catch: 9p is gone (§1), so this endpoint
  still needs either the HDV virtio-fs device-host (§1c) or a network CIFS/NFS share. Not free.
- **Boring-enterprise-ideal:** rebuild the cage as a **bootc image** (`FROM` Rocky/RHEL bootc,
  `bootc-image-builder` → disk), with a **UBI** agent-payload layer. Larger rearchitecture, but the
  genuinely enterprise-native target state — and the better article.

## Recommended spike (throwaway, stop at first wall)

Branch; don't touch the working Ubuntu build.

0. **9p kernel-config check (C2) — DONE.** In a `rockylinux:10` build, `dnf install -y kernel
   kernel-modules-extra`, then `grep -E 'CONFIG_(NET_9P|9P_FS)'` and `find /lib/modules -name '9p*'`.
   **Run 2026-06-01 — result: ABSENT** (`# CONFIG_NET_9P is not set`, no `9p*` modules, §1). Plan9 is
   off the table for a stock-kernel EL10 guest.
0b. **virtio-fs proof (C2, the real fix) — DONE, PASS (2026-06-01).** §1b's "no virtio-fs on HCS"
   was overturned (§1c); this step closes it empirically. A prebuilt `openvmm.exe` (OpenVMM CI
   `x64-windows-openvmm`) booted the **stock Rocky 10.1 kernel** (`6.12.0-211.16.1.el10_2.0.1.x86_64`,
   distro bzImage, as shipped) on WHP, with the host folder attached as `--virtio-fs
   pcie_port=fs:ws,<path>` on a PCIe root port. The stock in-tree `virtiofs.ko`/`fuse.ko` loaded, the
   device enumerated (`virtio-pci 0000:01:00.0 ... virtio0/device:0x001a`), `mount -t virtiofs ws
   /mnt` succeeded, the guest **read** the host sentinel **and wrote** a file back that appeared on the
   Windows host — bidirectional. So the EL10 guest driver + OpenVMM's virtio-fs device + WHP all work
   together on this box; HDV-in-broker integration is now bounded work against a known-good target. Two
   observed caveats: virtio-fs needs an explicit PCIe root complex/port (OpenVMM defaults the guest
   cmdline to `pci=off`), and `virtio_fs_setup_dax: No cache capability` (no DAX fast-path — functional
   virtio-fs, matches §1c). Full transcript: §1c. (See full detail below.)
1. **Manifest proof:** copy `rootfs/Dockerfile` → a Rocky variant, `FROM rockylinux:10`, translate the
   package lines, resolve `ripgrep`/`python3-seccomp`/`iproute` for real. `docker build` it. Settles
   every mechanical unknown in ~30 min.
2. **Kernel/initramfs proof (C1):** in that image, `dnf install kernel dracut dracut-config-generic`;
   confirm `/boot/vmlinuz-*` + `/boot/initramfs-*.img`; `find /lib/modules` / `lsinitrd` for the
   required modules. Answers C1 *without booting*.
3. **Path patches:** update `build.sh:219` + `fetch-kernel.sh` for `initramfs-*.img`; write
   `/etc/dracut.conf.d/atelier.conf` only if `dracut-config-generic` isn't enough.
4. **The gate — boot it:** build darwin-arm64-vz, `npm run e2e:host`. Then the separate HCS/Windows
   boot on a real Windows host.

Steps 0–2 retire most of the risk on paper and on a laptop. Step 4 is the only thing that can say
"yes." A death at step 0 or 2 is itself a publishable enterprise-integration finding — which is the
point of the research ground.

## Sources

- Does Rocky Linux include 9p filesystem? — Rocky Linux Forum — <https://forums.rockylinux.org/t/does-rocky-linux-include-9p-filesystem/15821>
- RHEL 9 file systems & storage considerations (virtiofs vs 9p) — <https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/9/html/considerations_in_adopting_rhel_9/assembly_file-systems-and-storage_considerations-in-adopting-rhel-9>
- virtio-fs project — <https://virtio-fs.gitlab.io/>
- Supported CentOS and RHEL VMs on Hyper-V — Microsoft Learn — <https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/supported-centos-and-red-hat-enterprise-linux-virtual-machines-on-hyper-v>
- How to install Red Hat provided Hyper-V daemons — <https://access.redhat.com/solutions/2949891>
- Generating a custom minimal bootc base image — RHEL 10 image mode — <https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/using_image_mode_for_rhel_to_build_deploy_and_manage_operating_systems/generating-a-custom-minimal-base-image>
- Introducing image mode for RHEL and bootable containers — <https://developers.redhat.com/articles/2024/05/07/image-mode-rhel-bootable-containers>
- bootc-image-builder — osbuild — <https://osbuild.org/docs/bootc/>
- (Re)Introducing the Red Hat Universal Base Image — <https://www.redhat.com/en/blog/introducing-red-hat-universal-base-image>

### HCS share-device research (2026-06-01, §1b)

- HCS Linux share gated to Plan9; VSMB gated Windows-only — microsoft/hcsshim `internal/uvm/{plan9,vsmb}.go` — <https://github.com/microsoft/hcsshim/blob/main/internal/uvm/vsmb.go>
- HCS schema device inventory (Plan9 + VirtualSmb, no virtio-fs) — <https://pkg.go.dev/github.com/Microsoft/hcsshim/internal/hcs/schema2>
- Linux 9p transports (trans_fd/virtio/rdma/xen/usbg; no VMBus transport) — kernel.org — <https://www.kernel.org/doc/html/latest/filesystems/9p.html>
- Linux VMBus driver (no SMB redirector; SMB client is TCP/RDMA only) — <https://docs.kernel.org/virt/hyperv/vmbus.html>
- WSL2 virtio-fs via private `wsldevicehost` vdev + custom kernel (device GUIDs) — microsoft/WSL PR #13822 — <https://github.com/microsoft/WSL/pull/13822>
- Kata Containers virtio-fs over KVM-only hypervisors (not Hyper-V) — <https://github.com/kata-containers/kata-containers/blob/main/docs/design/virtualization.md>
- Docker Desktop WSL2 backend uses 9p — <https://www.docker.com/blog/new-docker-desktop-wsl2-backend/>
