# VM Security Assessment

**Date:** 2026-05-22  
**Time:** 01:52 UTC  
**Assessor:** Claude (Internal Automated Audit)  
**Target:** Sandboxed Linux VM (HyperV Guest)  
**Scope:** Internal VM security posture — process isolation, network filtering, filesystem, kernel hardening, secrets handling  

---

## Executive Summary

A security assessment of the sandboxed Linux VM identified **17 findings** across three severity levels. The overall security posture is **critically weak** at the VM internals level despite a reasonably well-configured network filter at the hypervisor boundary.

The most severe issues are: the agent process running as **unconstrained root** with all Linux capabilities, the **entire filesystem being world-writable** (including system binaries and `/etc/passwd`), the **API key being fully exposed** in environment variables readable by every process, and **no seccomp or namespace isolation** to contain a compromised process.

The network-layer (vNIC) isolation is functioning correctly and is the strongest control in place. Everything behind it needs significant hardening.

| Severity | Count |
|----------|-------|
| 🔴 Critical | 6 |
| 🟠 High | 6 |
| 🟡 Medium | 3 |
| 🟢 Informational / Pass | 8 |

---

## Remediation Status — 2026-05-22

Remediation was implemented after this assessment and **verified on a live Hyper-V boot**
(create → start → in-guest checks → a real end-to-end agent run). Fixes touch the image
build (`image/build.sh`, `image/imager/`, `image/rootfs/Dockerfile`, `image/guest/init.sh`)
and the in-guest daemon / launcher (`services/cmd/guestd`, `services/internal/vmm`).

### Status at a glance

| ID | Title | Status |
|----|-------|--------|
| CRIT-01 | Unconstrained root + all capabilities | ✅ Fixed & verified |
| CRIT-02 | No seccomp filtering | ⏳ Deferred (bwrap can carry it) |
| CRIT-03 | Zero namespace isolation | ✅ Substantially addressed (agent) |
| CRIT-04 | API key in env / process memory | ⏳ Deferred — **rotate now** |
| CRIT-05 | World-writable system filesystem | ✅ Fixed & verified |
| HIGH-03 | SSH host keys world-writable | ✅ Resolved (SSH removed) |
| HIGH-06 | Port 80 open on vNIC filter | ⏳ Deferred |
| HIGH-01/02/04/05, MED-01/02/03 | (resource/audit/devices/tenant/kernel) | ◻ Not yet addressed |

### What was implemented

**CRIT-05 — world-writable system filesystem → fixed.**
Root cause: the build extracted the rootfs and ran `mke2fs -d` on the **Windows** host, which
cannot preserve Unix owner/mode, so everything landed world-writable. The ext4 is now populated
**inside a Linux "imager" container**; only the opaque ext4 blob crosses to Windows. Sensitive
perms are normalized at build (`/usr`,`/etc` `0755 root:root`; `/etc/passwd` `0644`;
`/etc/shadow` `0640`). The root disk is additionally mounted **read-only** (`RootFSReadOnly` →
SCSI `ReadOnly` + `ro` cmdline); the few writable paths are tmpfs with explicit,
non-world-writable modes (`/run`,`/sessions` `0755`; `/home/atelier` `0700`; `/var/tmp` `1777`;
`/tmp` per-sandbox `0755`).
*Verified in-guest:* `/usr/bin/bash` `0755 root:root`, `/etc/shadow` `0640`, `/` is `ro`,
write to `/usr/bin` → `EROFS`.

**CRIT-01 — unconstrained root → fixed.**
The agent is launched inside **bubblewrap** as uid/gid **1001** with `--cap-drop ALL` and fresh
**user/pid/ipc/uts** namespaces (net deliberately shared so egress still works); the read-only
root is bind-mounted so system paths stay immutable. A subtle gap was caught during verification:
with guestd (PID 1, root) launching bwrap *as root*, bwrap's user namespace mapped sandbox-uid
1001 onto **host-uid-0** (`uid_map: 1001 0 1`) — so the agent reported uid 1001 but was DAC-root
and could read `/etc/shadow`. guestd now drops the child's **real** uid/gid to 1001
(`SysProcAttr.Credential`) *before* exec'ing bwrap (which is not setuid), so the namespace can
only map to host-1001 and host-uid-0 files become foreign.
*Verified in-guest:* `id` → `uid=1001`; `CapEff/Prm/Bnd = 0`; real/eff/saved/fs uid all 1001;
`cat /etc/shadow` → **Permission denied** (root-owned files appear as `nobody`); `/workspace`,
`/home/atelier`, `/tmp` writable; `/run`, `/sessions` denied; a real agent run completed
end-to-end (wrote `/workspace`, reached the model over egress).

**CRIT-03 — namespace isolation → substantially addressed (for the agent).**
bubblewrap gives the agent its own user, pid, ipc, uts and mount namespaces. The network
namespace is intentionally shared (the egress jail is the boundary). The trusted control-plane
processes (guestd, gvforwarder) still share namespaces among themselves.

**HIGH-03 — SSH host keys world-writable → resolved.**
`openssh-server` was removed from the image (an unused listener), so there are no host keys to
expose. (This also retires the inbound-service surface.)

### Networking change (affects Part 5)

DHCP was removed (`isc-dhcp-client` dropped). The guest now configures `tap0` **statically**
(192.168.127.2, gw .1, fixed MAC) and runs gvforwarder with `-preexisting`. The host-side
DNS-sinkhole + TCP allowlist is unchanged; egress allow/deny was re-verified
(`api.anthropic.com` reachable; other hosts `NXDOMAIN`).

### Deferred (still open)

- **CRIT-04** — API key still injected via env var. **Rotate the key now** (it was exposed
  during the assessment) and move to a tmpfs file / short-lived scoped key.
- **CRIT-02** — no seccomp yet; bubblewrap can carry a cBPF profile via `--seccomp` next.
- **HIGH-06** — port 80 still reachable post-DNS; gate the allowlist to 443.
- HIGH-01/02/04/05, MED-01/02/03 — not yet addressed.

### Host-side issues found during verification (not VM-internal findings)

- **broker `stopVM` reported a phantom HCS error** (`HcsCloseComputeSystem hresult=0x8f3a51f0`):
  the binding interpreted a return value from `HcsCloseComputeSystem`, which is documented
  `void`. **Fixed** (binding no longer reads the return); `stopVM` now returns cleanly.
- **broker can panic on VM teardown** (`linuxkit/virtsock` hvsock `ioCompletionProcessor` —
  "The handle is invalid") when a VM is stopped while its egress connection is mid-handshake.
  Pre-existing third-party-library race; **open**.

---

## Environment

This table captures the original audited VM environment. The remediation status above notes the
post-audit changes, including the agent's uid/gid 1001 bubblewrap launch path and the read-only
root filesystem.

| Property | Value |
|----------|-------|
| OS | Ubuntu 22.04.5 LTS (Jammy Jellyfish) |
| Kernel | 6.8.0-117-generic |
| Hypervisor | Microsoft Hyper-V (VMBus 5.3) |
| Architecture | x86_64 |
| Hostname | (none) — scrubbed |
| Network Interface | tap0 — 192.168.127.2/24 |
| Network Transport | gvforwarder → vsock://2:1024 → HyperV host proxy |
| Init Process | /usr/sbin/guestd (custom, not systemd) |
| Installed Packages | 199 |
| Running User | root (uid=0, gid=0) |

---

## Part 1 — Process & Privilege Isolation

### CRIT-01 — Process Running as Unconstrained Root

**Severity:** 🔴 Critical  
**Category:** Privilege Isolation

> ✅ **Status (2026-05-22): Fixed & verified.** Agent runs under bubblewrap as uid/gid 1001,
> `--cap-drop ALL` (CapEff/Prm/Bnd = 0), with user/pid/ipc/uts/mnt namespaces. guestd drops the
> child's real uid to 1001 before exec so the userns can't map to host-root — `cat /etc/shadow`
> is now denied. See [Remediation Status](#remediation-status--2026-05-22).

The Claude agent and all child processes run as `uid=0 gid=0 (root)` with **all 41 Linux capabilities** granted and no restrictions.

```
uid=0(root)  gid=0(root)  groups=0(root)
CapEff: 000001ffffffffff   (all capabilities)
NoNewPrivs: 0
Seccomp: 0
```

Capabilities of particular concern:

| Capability | Risk |
|------------|------|
| `CAP_SYS_MODULE` | Load arbitrary kernel modules (rootkit insertion) |
| `CAP_SYS_ADMIN` | Mount filesystems, manipulate namespaces, bypass many controls |
| `CAP_SYS_PTRACE` | Read/write memory of any process, including the agent itself |
| `CAP_SYS_RAWIO` | Direct hardware access (disk, memory ports) |
| `CAP_SYS_BOOT` | Reboot or kexec a new kernel |
| `CAP_MAC_OVERRIDE` | Bypass AppArmor/SELinux if later added |
| `CAP_NET_ADMIN` | Reconfigure network interfaces, routing, firewall rules |
| `CAP_DAC_OVERRIDE` | Bypass all filesystem permission checks |

**Impact:** Any code executing in this VM (including via prompt injection) runs with the highest possible privilege. There is no privilege boundary to cross.

**Remediation:**
```
- Run agent process as a dedicated non-root user (e.g. uid=1001)
- Drop all capabilities: --cap-drop=ALL
- Re-add only what is genuinely required (likely none)
- Set NoNewPrivs=1 via prctl(PR_SET_NO_NEW_PRIVS) in the launcher
```

---

### CRIT-02 — No Seccomp Filtering

**Severity:** 🔴 Critical  
**Category:** Syscall Filtering

> ⏳ **Status (2026-05-22): Deferred.** Not yet applied. bubblewrap (now in the launch path) can
> carry a cBPF profile via `--seccomp` as the next step. See [Remediation Status](#remediation-status--2026-05-22).

No seccomp profile is applied. All ~350 Linux syscalls are available to every process.

```
Seccomp: 0
Seccomp_filters: 0
```

Combined with full root capabilities, this means dangerous syscalls such as `ptrace`, `process_vm_readv`, `kexec_load`, `init_module`, `finit_module`, `iopl`, and `ioperm` are all freely callable.

**Impact:** A compromised process can perform any kernel operation. Seccomp is the last line of defence when capabilities are broad — here both are absent simultaneously.

**Remediation:**
```
- Apply Docker's default seccomp profile as a minimum baseline
- Additionally block: ptrace, process_vm_readv/writev, kexec_load,
  init_module, finit_module, iopl, ioperm, create_module, delete_module
- Ideally use a custom allowlist profile tuned to what node/claude actually needs
```

---

### CRIT-03 — Zero Namespace Isolation Between Processes

**Severity:** 🔴 Critical  
**Category:** Process Isolation

> ✅ **Status (2026-05-22): Substantially addressed for the agent.** The agent now runs in its
> own user/pid/ipc/uts/mnt namespaces via bubblewrap (net namespace shared by design — the egress
> jail is the boundary). Control-plane processes still share namespaces.
> See [Remediation Status](#remediation-status--2026-05-22).

Every process in the VM — `guestd`, `gvforwarder`, `node`, `claude-agent`, shells — shares **all namespaces** with PID 1. There is no internal isolation boundary.

```
cgroup:  SHARED with PID 1
ipc:     SHARED with PID 1
mnt:     SHARED with PID 1
net:     SHARED with PID 1
pid:     SHARED with PID 1
time:    SHARED with PID 1
user:    SHARED with PID 1
uts:     SHARED with PID 1
```

**Impact:** Any process can observe and interact with every other process's filesystem view, network stack, IPC objects, and process tree without any kernel-enforced boundary.

**Remediation:**
```
- Run the agent in a dedicated mount namespace (separate mnt ns)
- Use a PID namespace so the agent cannot see or signal system processes
- Consider a user namespace to further limit capability scope
```

---

### HIGH-01 — No Cgroup Resource Limits

**Severity:** 🟠 High  
**Category:** Resource Isolation

No memory or CPU limits are applied via cgroups.

```
Memory limit: none (unlimited)
CPU quota:    none (unlimited)
Max processes: 7,277 (system default only)
```

**Impact:** A runaway or adversarial workload can exhaust all VM memory (OOM killing the agent or guestd) or consume 100% CPU indefinitely, causing denial of service for this and potentially co-resident sessions.

**Remediation:**
```
- Set memory.max in the agent's cgroup slice (e.g. 2G)
- Set cpu.max to cap CPU usage (e.g. 150% of one core)
- Apply via systemd slice, cgroupv2 directly, or container runtime limits
```

---

### HIGH-02 — No Audit Logging

**Severity:** 🟠 High  
**Category:** Monitoring & Detection

`auditd` is not running. There is no syscall-level audit trail, no process execution logging, and no file access recording.

```
/var/log/ contains: alternatives.log, apt/, bootstrap.log, btmp,
                    dpkg.log, faillog, lastlog, wtmp
No: auth.log, syslog, audit/audit.log
```

**Impact:** Malicious activity inside the VM is completely invisible. There is no forensic trail to reconstruct what happened after an incident.

**Remediation:**
```
- Enable auditd with rules covering: execve, open/openat on sensitive paths,
  ptrace, network connect, module load
- Forward logs to the host via vsock or a log drain before the VM can tamper with them
- At minimum, enable kernel process accounting
```

---

## Part 2 — Secrets & Credentials

### CRIT-04 — API Key Exposed in Environment Variables of All Processes

**Severity:** 🔴 Critical  
**Category:** Secrets Management

> ⏳ **Status (2026-05-22): Deferred — action required.** Still injected via env var. **Rotate the
> exposed key now** and move to a tmpfs file / short-lived scoped key. The non-root + read-only
> hardening (CRIT-01/05) narrows but does not remove this exposure. See [Remediation Status](#remediation-status--2026-05-22).

The `ANTHROPIC_API_KEY` is injected as an environment variable and is readable from `/proc/PID/environ` for every process in the VM.

```
ANTHROPIC_API_KEY=sk-ant-api03-[REDACTED]
```

The key was found readable in `/proc/environ` for PIDs: 1, 286, 298, 310, 373, 384, 396, 687, 698, 710 and every shell spawned during the session.

Additionally, a full memory scan (`/proc/PID/mem`) located the API key in **73 distinct memory regions** across all running processes — it is present in heap, stack, and anonymous mappings of the node runtime, the agent binary, and even PID 1 (guestd).

**Impact:** Any process executing in the VM — including code run via tool use or prompt injection — can trivially read the API key and exfiltrate it (if network controls were bypassed) or use it within the allowed network path.

**Remediation:**
```
- Do not inject secrets via environment variables
- Mount secrets via a tmpfs file readable only by the agent user
- Use a secrets socket / credential helper that issues short-lived tokens
- Rotate the key immediately as it was exposed during this assessment
- Consider per-session ephemeral keys that are revoked when the VM terminates
```

---

### HIGH-03 — SSH Host Private Keys World-Readable and World-Writable

**Severity:** 🟠 High  
**Category:** Credential Exposure

> ✅ **Status (2026-05-22): Resolved.** `openssh-server` was removed from the image, so no SSH
> host keys exist. See [Remediation Status](#remediation-status--2026-05-22).

All SSH host private keys in `/etc/ssh/` have `rwxrwxrwx` permissions (owned by uid 1000):

```
-rwxrwxrwx  /etc/ssh/ssh_host_ecdsa_key      (private)
-rwxrwxrwx  /etc/ssh/ssh_host_ed25519_key    (private)
-rwxrwxrwx  /etc/ssh/ssh_host_rsa_key        (private)
```

**Impact:** Any process can read the private host keys (facilitating host impersonation / MITM) or overwrite them with attacker-controlled keys.

**Remediation:**
```
chmod 600 /etc/ssh/ssh_host_*_key
chown root:root /etc/ssh/ssh_host_*_key
```

---

## Part 3 — Filesystem Security

### CRIT-05 — World-Writable System Filesystem

**Severity:** 🔴 Critical  
**Category:** Filesystem Integrity

> ✅ **Status (2026-05-22): Fixed & verified.** Root cause was building the ext4 on the Windows
> host (modes/owner lost); it is now populated inside a Linux imager container with normalized
> perms (`/usr`,`/etc` 0755 root:root; `/etc/shadow` 0640). The root disk is mounted **read-only**
> and writable paths are tmpfs with explicit non-world-writable modes. Verified: `/usr/bin/bash`
> 0755 root, `/` is `ro`, writes → `EROFS`. See [Remediation Status](#remediation-status--2026-05-22).

The root filesystem (`/dev/sda` mounted as ext4) has extreme permission misconfiguration. System directories and binaries are world-writable:

```
drwxrwxrwx  /usr/bin/          ← entire bin directory writable
drwxrwxrwx  /etc/              ← entire etc directory writable
-rwxrwxrwx  /usr/bin/bash      ← shell binary replaceable
-rwxrwxrwx  /usr/bin/node      ← JS runtime replaceable
-rwxrwxrwx  /usr/bin/apt       ← package manager replaceable
-rwxrwxrwx  /etc/passwd        ← user database writable (uid 1000 owned)
-rwxrwxrwx  /etc/shadow        ← password hashes writable (uid 1000 owned)
-rwxrwxrwx  /etc/cron.d/       ← cron jobs writable
```

Root filesystem mount options: `rw,relatime` — no `nosuid`, `nodev`, or `noexec`.

**Impact:** This is a trivial persistence and backdoor vector. Any process (regardless of privilege) can replace `/usr/bin/bash` with a malicious binary, add a root account to `/etc/passwd`, or plant a cron job. This entirely undermines any other isolation measure.

**Impact is rated Critical** even though we are already root, because this means *any process at any privilege level* can achieve persistence and lateral impact.

**Remediation:**
```
- Fix permissions on the VM image at build time:
  chmod 755 /usr /usr/bin /usr/sbin /bin /sbin /etc
  chown root:root /etc/passwd /etc/shadow
  chmod 644 /etc/passwd && chmod 640 /etc/shadow

- Mount root filesystem with: ro (read-only) or at minimum nosuid,nodev
- Mount /tmp as a separate tmpfs with noexec,nosuid
- Consider an immutable root filesystem (overlayfs with read-only lower layer)
```

---

### HIGH-04 — Raw Disk and Physical Memory Accessible

**Severity:** 🟠 High  
**Category:** Hardware Access

Both `/dev/sda` (raw block device) and `/dev/mem` (physical memory) are present and readable:

```
/dev/sda   crw-r----- → readable (full disk image)
/dev/mem   crw-r----- → readable (physical RAM)
```

Confirmed readable:
```python
open('/dev/sda', 'rb').read(512)  → 512 bytes returned (MBR region)
open('/dev/mem', 'rb').read(256)  → 256 bytes returned (physical page 0)
```

**Impact:** With `CAP_SYS_RAWIO` (which is granted — see CRIT-01), an attacker can scan physical memory for secrets belonging to other VMs that previously occupied the same memory pages, or access disk sectors outside the normal filesystem layer.

**Remediation:**
```
- Bind-mount /dev/null over /dev/mem: mount --bind /dev/null /dev/mem
- Remove /dev/sda from the VM's /dev if direct disk access is not required
- The VM image builder should not expose these devices to guest VMs
```

---

### MEDIUM-01 — Cross-Session Filesystem Mount Exposure

**Severity:** 🟡 Medium  
**Category:** Tenant Isolation

Three separate session workspaces are mounted inside this single VM via 9p/VirtioFS:

```
/sessions/sfd33ae0290  ← this session
/sessions/s508f54daa8  ← foreign session (contains: Pentaho_Alteryx_Migration_Summary.docx,
                          create_summary_docx.py, pentaho_alteryx_markdown.md)
/sessions/saaa7109f96  ← foreign session (contains: Dbgview.exe, Dbgview64.exe, Eula.txt)
```

All three are mounted `rw,relatime` with no access restrictions.

**Impact:** This VM can read, modify, or delete files belonging to two other unrelated user sessions. If those sessions contain sensitive data or source code, it is fully exposed. This breaks the tenant isolation model.

**Remediation:**
```
- Each VM should receive only its own session mount
- If multiple sessions must share a VM, mount foreign sessions read-only
  and under a separate, unprivileged user namespace
- Audit why multiple sessions are being co-located in a single VM
```

---

## Part 4 — Kernel Hardening

### MEDIUM-02 — Kernel Pointer Addresses Exposed

**Severity:** 🟡 Medium  
**Category:** Kernel Hardening

```
kernel.kptr_restrict = 0
```

Kernel symbol addresses are visible in `/proc/kallsyms` and other interfaces to any process, including unprivileged ones.

**Impact:** Kernel pointer exposure significantly aids exploit development by defeating KASLR. An attacker can read exact kernel function and data structure addresses, removing the need to brute-force randomised layouts.

**Remediation:**
```
sysctl -w kernel.kptr_restrict=2
# Persist in /etc/sysctl.d/99-hardening.conf
```

---

### MEDIUM-03 — Kernel Module Loading Not Locked

**Severity:** 🟡 Medium  
**Category:** Kernel Hardening

```
kernel.modules_disabled = 0
```

New kernel modules can be loaded at runtime. Combined with `CAP_SYS_MODULE` (granted — see CRIT-01), this allows loading of arbitrary kernel code.

**Impact:** An attacker can insert a kernel rootkit (e.g. hiding processes, intercepting syscalls, exfiltrating data at kernel level) that survives any userspace security measure.

**Remediation:**
```
# Lock module loading after boot (one-way, cannot be undone without reboot):
sysctl -w kernel.modules_disabled=1

# Or use module signing enforcement:
# CONFIG_MODULE_SIG_FORCE=y in kernel config
```

---

## Part 5 — Network Filter (vNIC)

### Architecture

All VM network traffic flows through a user-space proxy chain:

```
VM (tap0) ──► gvforwarder (user-space, /dev/net/tun)
           ──► vsock://2:1024 (HyperV VMBus)
           ──► Host-side proxy (enforces allowlist policy)
           ──► Internet
```

The host-side vsock proxy implements a **DNS-sinkhole + TCP allowlist** pattern:
- Only `api.anthropic.com` resolves via the gateway DNS (all others return `NXDOMAIN`)
- TCP connections are only permitted after a DNS lookup for an allowed hostname
- Direct-by-IP TCP connections to any address receive an instant RST
- All UDP (except DHCP) is silently dropped

### PASS — TCP Blocking

All tested TCP destinations receive an instant RST (<1ms) from the filter:

| Destination | Result |
|-------------|--------|
| 1.1.1.1:443 (Cloudflare) | RST (0.3ms) |
| 8.8.8.8:53 (Google DNS) | RST (0.3ms) |
| 8.8.8.8:443 | RST (0.4ms) |
| 10.0.0.1:22 (RFC1918) | RST (0.3ms) |
| 172.16.0.1:22 (RFC1918) | RST (0.3ms) |
| 192.168.1.1:80 (RFC1918) | RST (0.2ms) |
| 192.168.127.1:22 (Gateway) | RST (0.2ms) |
| 160.79.104.10:22 (API host, wrong port) | RST (0.2ms) |

### PASS — UDP Blocking

External UDP (DNS to 8.8.8.8, NTP to 1.1.1.1) is silently dropped.

### PASS — DNS Sinkhole

Only `api.anthropic.com` resolves. All other domains return `NXDOMAIN`.

### FINDING — Port 80 (HTTP) Is Open Alongside 443

> ⏳ **Status (2026-05-22): Deferred (HIGH-06).** Allowlist not yet restricted to 443.
> See [Remediation Status](#remediation-status--2026-05-22).

After resolving `api.anthropic.com` via the gateway DNS, TCP port **80 is reachable** as well as 443:

```
api.anthropic.com:443 → OPEN (5.9ms)
api.anthropic.com:80  → OPEN (6.3ms)
```

**Impact:** If the API endpoint ever accepts or reflects requests over plaintext HTTP, traffic would be unencrypted through the vsock tunnel. Port 80 serves no purpose for an HTTPS API.

**Remediation:**
```
Restrict the TCP allowlist to port 443 only.
Block outbound TCP:80 to all destinations.
```

### FINDING — ICMP is Spoofed Locally for All Destinations

ICMP echo requests return instant replies (0.000s) for **every IP address tested**, including RFC 5737 documentation addresses that do not exist on the internet:

```
192.0.2.1   (TEST-NET-1, RFC5737):  ECHO_REPLY from 192.0.2.1  (0.000s)
198.51.100.1 (TEST-NET-2, RFC5737): ECHO_REPLY from 198.51.100.1 (0.000s)
10.0.0.1    (RFC1918):              ECHO_REPLY from 10.0.0.1    (0.000s)
1.1.1.1     (Cloudflare):           ECHO_REPLY from 1.1.1.1     (0.000s)
```

The `gvforwarder` user-space network stack is generating fake ICMP echo replies on behalf of all destinations without forwarding the packets. Ping always succeeds even for completely unreachable hosts.

**Impact:** Misleading — operators or monitoring tools using ping to test connectivity will always see "up" regardless of actual reachability. Masks network partitions and filter policy from inside the VM.

**Remediation:**
```
Either:
  a) Drop ICMP outright (silently) — most appropriate for a locked-down VM
  b) Forward ICMP honestly and only respond to pings that actually succeed
Do not generate fake ICMP replies.
```

---

## Part 6 — Passing Controls

The following were tested and found to be correctly configured:

| Control | Status | Detail |
|---------|--------|--------|
| ASLR | ✅ Pass | `kernel.randomize_va_space=2` (full) |
| dmesg restriction | ✅ Pass | `kernel.dmesg_restrict=1` |
| Perf event restriction | ✅ Pass | `kernel.perf_event_paranoid=4` |
| Unprivileged BPF | ✅ Pass | `kernel.unprivileged_bpf_disabled=2` |
| SUID dumpable | ✅ Pass | `fs.suid_dumpable=0` |
| SUID/SGID binaries | ✅ Pass | None found on filesystem |
| SYN cookies | ✅ Pass | `net.ipv4.tcp_syncookies=1` |
| No listening ports | ✅ Pass | Only UDP:68 (DHCP client) — no inbound services |
| No IPC shared memory | ✅ Pass | No shared memory segments or semaphore arrays |
| No /dev/kmem | ✅ Pass | Device not present |
| No KVM device | ✅ Pass | `/dev/kvm` not present — no nested virtualisation |
| Hostname scrubbed | ✅ Pass | Hostname is `(none)` |
| Minimal package footprint | ✅ Pass | 199 packages installed |
| Outbound network restricted | ✅ Pass | Only `api.anthropic.com:443` reachable (see Part 5) |

---

## Consolidated Finding Index

| ID | Severity | Title | CVSS Approximate |
|----|----------|-------|-----------------|
| CRIT-01 | 🔴 Critical | Unconstrained root with all capabilities | 9.8 |
| CRIT-02 | 🔴 Critical | No seccomp syscall filtering | 9.3 |
| CRIT-03 | 🔴 Critical | Zero namespace isolation between processes | 8.8 |
| CRIT-04 | 🔴 Critical | API key exposed in env vars & process memory (73 regions) | 9.1 |
| CRIT-05 | 🔴 Critical | World-writable system filesystem incl. /usr/bin/bash, /etc/passwd | 9.6 |
| HIGH-01 | 🟠 High | No cgroup resource limits (memory/CPU) | 6.5 |
| HIGH-02 | 🟠 High | No audit logging or forensic trail | 6.2 |
| HIGH-03 | 🟠 High | SSH host private keys world-readable and world-writable | 7.4 |
| HIGH-04 | 🟠 High | /dev/mem and /dev/sda directly readable | 7.1 |
| HIGH-05 | 🟠 High | Cross-session filesystem mount (other users' files readable) | 7.5 |
| HIGH-06 | 🟠 High | Port 80 open on vNIC filter alongside 443 | 5.9 |
| MED-01 | 🟡 Medium | kernel.kptr_restrict=0 (kernel addresses exposed) | 5.3 |
| MED-02 | 🟡 Medium | kernel.modules_disabled=0 (runtime module loading allowed) | 5.9 |
| MED-03 | 🟡 Medium | ICMP spoofed locally for all destinations | 3.7 |

---

## Remediation Priority Matrix

```
┌─────────────────────────────────────────────────────────────────┐
│ P0 — Fix before next session (breaks isolation model)           │
│                                                                 │
│  1. chmod 755 /usr /usr/bin /usr/sbin /etc                      │
│     chmod 644 /etc/passwd && chmod 640 /etc/shadow              │
│     chmod 600 /etc/ssh/ssh_host_*_key                           │
│                                                                 │
│  2. Run agent as non-root: --user 1001:1001                     │
│                                                                 │
│  3. Drop all capabilities: --cap-drop=ALL                       │
│     Add back only what's needed (likely none)                   │
│                                                                 │
│  4. Rotate the exposed ANTHROPIC_API_KEY immediately            │
│     Use per-session ephemeral keys going forward                │
│                                                                 │
│  5. Mount each VM with its own session only (fix HIGH-05)       │
├─────────────────────────────────────────────────────────────────┤
│ P1 — This sprint                                                │
│                                                                 │
│  6. Add seccomp profile (Docker default as baseline)            │
│                                                                 │
│  7. Inject secrets via tmpfs file, not env var                  │
│                                                                 │
│  8. Set NoNewPrivs=1 in agent launcher                          │
│                                                                 │
│  9. Add cgroup memory + CPU limits                              │
│                                                                 │
│ 10. sysctl -w kernel.kptr_restrict=2                            │
│     sysctl -w kernel.modules_disabled=1  (post-boot)           │
│                                                                 │
│ 11. Restrict vNIC filter to TCP:443 only (drop port 80)         │
│                                                                 │
│ 12. Remove /dev/mem from VM device list                         │
│     Remove /dev/sda if direct disk access not required          │
├─────────────────────────────────────────────────────────────────┤
│ P2 — Housekeeping                                               │
│                                                                 │
│ 13. Enable auditd, forward logs to host via vsock               │
│                                                                 │
│ 14. Add mnt + pid namespace isolation for agent process         │
│                                                                 │
│ 15. Fix ICMP spoofing in gvforwarder (drop or forward honestly) │
│                                                                 │
│ 16. Mount root filesystem read-only or with nosuid,nodev,noexec │
│     Use tmpfs overlay for runtime writes                        │
└─────────────────────────────────────────────────────────────────┘
```

---

## Technical Notes

### API Key Redaction
The `ANTHROPIC_API_KEY` value was observed during this assessment and has been redacted in this report. **The key should be treated as compromised and rotated immediately** — it was readable from `/proc/*/environ` by any process in the VM and found in 73 memory regions via `/proc/*/mem`.

### Network Filter Mechanism
The vNIC filter operates as a user-space TCP proxy (`gvforwarder`) connected via HyperV VMBus vsock (CID 2, port 1024) to a host-side policy engine. The filter uses a DNS-sinkhole approach: only resolving approved hostnames, then permitting TCP connections whose destination IP was established via an approved DNS lookup. Direct-IP TCP connections are blocked with RST injection.

### Assessment Methodology
All tests were conducted from within the VM using Python 3 standard library socket operations, `/proc` filesystem reads, and the `curl` binary (the only network utility present). No external tooling, exploit frameworks, or network scanning tools were used or available.

---

*Report generated by automated security assessment — Claude Agent SDK VM*  
*Classification: Internal / Security Sensitive*
