# Migrating the cage to Rocky Linux EL10

| Field | Detail |
|---|---|
| Status | Research / decision record, May 2026. No code changed. |
| Primary reader | Engineers evaluating a future utility-VM rootfs move from Ubuntu 24.04 to Rocky Linux EL10. |
| Decision | Target Rocky Linux EL10 for the spike. |
| Main risk | RHEL-family kernels likely omit 9p, so Windows/HCS file sharing may need virtiofs. |

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

**Honest caveat:** confirmed for EL9/Rocky 9 only. EL10 is too new for a definitive public kernel
config, but the direction is firmly set; treat "EL10 also ships without 9p" as the working assumption
until the spike's `lsinitrd` / `modprobe` step proves otherwise.

**Why it hits Atelier directly.** `image/guest/init.sh` modprobes `9pnet_virtio`/`9p`, and host↔guest
shares (`/workspace`, per-session `/sessions/<tag>`) ride 9p where virtiofs isn't available. Per the
design, **virtiofs is the VZ/macOS path; 9p is the home for WSL2 / client Hyper-V** — i.e. exactly the
**Windows/HCS target** we most want to harden. On a RHEL cage the `9p*` modprobes simply fail (modules
absent), and any Windows-side 9p share must move to **virtiofs**. That likely reaches the **Go broker**
(`services/internal/vmm`, the HCS share wiring), not just the Dockerfile — so it is a *design* change,
not a substitution. This is the single biggest risk and the most article-worthy friction: *the
enterprise distro won't mount the share mechanism the enterprise hypervisor pairs with.*

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
| C2 | **9p → virtiofs** for host↔guest shares | `image/guest/init.sh` (9p modprobes) **+ likely `services/internal/vmm` HCS share path** | See finding #1. The one change that may leave the image tree. **Step 0 of the spike.** |
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

1. **9p → virtiofs on the Windows/HCS share path (C2).** The make-or-break. If the Windows host can't
   present the workspace to a RHEL guest over virtiofs (or 9p truly is dead on EL10 and there's no
   in-tree alternative), the migration stalls regardless of how clean the Dockerfile rewrite is.
   Reaches the Go broker, not just the image. **Resolve this first.**
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
  `kernel`, virtiofs shares). Smallest diff from today; keeps the build we know.
- **Boring-enterprise-ideal:** rebuild the cage as a **bootc image** (`FROM` Rocky/RHEL bootc,
  `bootc-image-builder` → disk), with a **UBI** agent-payload layer. Larger rearchitecture, but the
  genuinely enterprise-native target state — and the better article.

## Recommended spike (throwaway, stop at first wall)

Branch; don't touch the working Ubuntu build.

0. **Share proof (C2):** prove the Windows/HCS host can share the workspace to a RHEL guest over
   **virtiofs**, or confirm 9p is unavailable on EL10. Gates everything.
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
