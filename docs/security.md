# VM Sandbox Security

Scope: securing the **guest VM as the containment boundary** for an autonomous Claude agent
that works on local files. The App-to-Broker IPC boundary is covered separately in
[`ipc-security.md`](ipc-security.md).

This document consolidates the live security assessment findings, remediation history, and the
forward-looking hardening backlog. It tracks what is implemented, what remains open, and why the
order matters.

---

## Assessment Timeline

| Date | Environment | Findings | Notes |
|------|-------------|----------|-------|
| 2026-05-22 | Ubuntu 22.04.5 LTS · Linux 6.8.0 · x86_64 · Hyper-V | 6 critical, 6 high, 3 medium | Initial assessment |
| 2026-05-22 | (same) | — | Remediations applied and verified on live HCS boot |
| 2026-05-24 | Ubuntu 24.04.4 LTS · Linux 6.17.0 · aarch64 · Apple Silicon VZ VM | 1 critical, 3 high, 3 medium | Follow-up assessment post-remediation |

**Assessor:** Claude (sandboxed agent)

---

## Current Posture

The isolation spine is strong: Atelier uses a Hyper-V utility VM, a default-deny host-mediated
egress path, a read-only root disk, and bubblewrap for the agent process. The remaining risk is
concentrated in key residency, syscall filtering, resource limits, and shared-VM session separation.

| Area | Current state | Code reference |
|---|---|---|
| Hypervisor boundary | Dedicated Linux utility VM driven by HCS | `services/internal/hcs`, `services/internal/vmm` |
| Agent identity | Agent launched as uid/gid 1001, not root | `services/cmd/guestd/sandbox_linux.go` |
| Agent process sandbox | bubblewrap user/pid/ipc/uts/mount namespaces; caps dropped | `services/cmd/guestd/sandbox_linux.go` |
| Root filesystem | rootfs mounted read-only; writable paths are tmpfs/session shares | `services/internal/hcs/doc.go`, `image/guest/init.sh` |
| Egress | runtime hostname allowlist with DNS pinning; default deny | `services/internal/netjail` |
| Model credential | still passed into the in-guest process environment | `apps/desktop/src/main/sessions/manager.ts`, `packages/agent/src/cli-guest.ts` |
| Seccomp | not applied yet | open |
| Resource limits | no cgroup limits yet | open |

### Finding Summary (2026-05-24)

| Severity | Count |
|---|---|
| Critical | 1 |
| High | 3 |
| Medium | 3 |

---

## Open Findings (2026-05-24)

### Critical

#### 1. Unprivileged User Namespace Creation Grants Full Capabilities

**Command:**
```bash
unshare --user --map-root-user id
# → uid=0(root) gid=0(root) groups=0(root)

unshare --user --map-root-user cat /proc/self/status | grep Cap
# CapPrm: 000001ffffffffff  ← ALL capabilities
# CapEff: 000001ffffffffff  ← ALL capabilities effective
# CapBnd: 000001ffffffffff  ← ALL capabilities in bounding set
```

Any process inside the sandbox can create a new user namespace and gain all Linux capabilities
within that namespace as apparent root. With no seccomp filter, this is a meaningful attack surface:

- Can be used to exploit kernel vulnerabilities that require `CAP_SYS_ADMIN` or similar
- With `CAP_NET_ADMIN` inside a user namespace + new network namespace, can manipulate network stack
- Unprivileged user namespaces are historically the primary source of kernel privilege escalation
  CVEs on Linux (e.g. Dirty Pipe, various overlayfs escapes)

The new user namespace is still subject to the outer kernel's security boundary — capabilities are
scoped to the namespace — but combined with no seccomp and a potentially vulnerable kernel, it is a
viable attack chain.

**Fix:**
```bash
# Option A: Disable unprivileged user namespaces entirely
echo 0 > /proc/sys/kernel/unprivileged_userns_clone
# or in sysctl.conf:
kernel.unprivileged_userns_clone = 0

# Option B: Restrict via seccomp
# Block: CLONE_NEWUSER flag in clone()/unshare() syscalls via seccomp profile
```

---

### High

#### 2. `guestd` Binary and Mount Visible Inside Sandbox

The `guestd` volume (`/dev/vdb /opt/guestd ext4 ro,nosuid,nodev,relatime`) is visible from inside
the bwrap sandbox. The `guestd` ELF binary (4 MB, Go binary) is fully readable, including via a user
namespace.

Root cause: `--bind / /` in bwrap exposes all mounts. An attacker can enumerate host communication
infrastructure and analyze the binary to understand the host communication protocol.

**Fix:** Add `--tmpfs /opt/guestd` after `--bind / /` in the bwrap invocation. bwrap processes bind
arguments in order, so a later entry shadows the earlier one for that path.

```bash
bwrap \
  --bind / / \
  --tmpfs /opt/guestd \   # shadows the real mount with an empty tmpfs
  ...
```

**Verification:** Re-run `cat /proc/mounts` from inside a new session to confirm the shadow.

---

#### 3. Agent Runtime Source Code Fully Readable

The entire `/opt/atelier` tree is readable from inside the sandbox:

```
/opt/atelier/packages/agent/src/seams/policy.ts     ← full policy engine logic
/opt/atelier/packages/agent/src/broker/client.ts     ← broker client
/opt/atelier/packages/agent/src/cli-guest.ts         ← guest entrypoint
/opt/atelier/packages/protocol/schema/protocol.json  ← full RPC protocol schema
/opt/atelier/packages/provider/src/index.ts          ← provider source
```

This exposes the complete policy evaluation logic (GUEST_ALLOW/GUEST_DENY sets, door
classification), the full broker RPC protocol, and implementation details that aid targeted
exploitation. The `setEgressPolicy` RPC is particularly notable — if reachable, it could be used
to whitelist network egress.

**Fix:** Add `--tmpfs /opt/atelier` after `--bind / /` in the bwrap invocation (same pattern as
the guestd fix).

---

#### 4. `kptr_restrict = 0` — Kernel Pointer Restriction Disabled

```bash
cat /proc/sys/kernel/kptr_restrict  # → 0
```

Kernel pointers in `/proc/kallsyms` and other interfaces are not restricted. `/proc/kallsyms`
currently returns all-zero addresses (likely due to kernel ASLR + user namespace mapping), but the
setting may leak pointers through other interfaces (`/proc/net`, dmesg, etc.).

**Fix:**
```bash
# sysctl.conf:
kernel.kptr_restrict = 2   # hide from all users including root
```

---

### Medium

#### 5. No Seccomp Filter

```
Seccomp: 0
Seccomp_filters: 0
```

No syscall filtering is applied. Combined with user namespace capability escalation (#1), this
significantly widens the attack surface for kernel exploits.

**Fix:** Apply a seccomp profile to the bwrap invocation:
```bash
bwrap --seccomp <fd> ...
# Use a profile derived from Docker's default seccomp policy,
# additionally blocking: unshare, clone (CLONE_NEWUSER), keyctl,
# perf_event_open, bpf, userfaultfd
```

---

#### 6. Speculation Store Bypass Vulnerable

```
Speculation_Store_Bypass: vulnerable
SpeculationIndirectBranch: unknown
```

The VM is not applying speculative execution mitigations (SSBD). On Apple Silicon this is lower
risk than x86, but worth noting for multi-tenant scenarios.

**Fix:** Add `spec_store_bypass_disable=seccomp` or `spec_store_bypass_disable=on` to kernel
cmdline. Currently booted with: `console=hvc0 root=/dev/vda ro noresume init=/sbin/init`.

---

#### 7. Sensitive Data in Readable Home Directory

```
/home/atelier/.claude.json            → contains userID hash
/home/atelier/.claude/policy-limits.json  → reveals restriction config
```

The `userID` field and full policy-limits configuration are readable. These files are owned by
`atelier` (the agent user) so the agent needs to read them. Consider moving policy-limits out of
the agent-readable path or encrypting sensitive fields.

---

## Good Controls

| Control | Status |
|---|---|
| Running as unprivileged user (uid=1001) | ✅ |
| All capabilities dropped on main process (`CapEff: 0`) | ✅ |
| `NoNewPrivs = 1` on main process | ✅ |
| No listening network ports | ✅ |
| Root filesystem mounted read-only (`ro`) | ✅ |
| User/PID/IPC/UTS/MNT namespace isolation via bwrap | ✅ |
| `sudo` not installed | ✅ |
| No SUID binaries found | ✅ |
| `dmesg_restrict = 1` | ✅ |
| `perf_event_paranoid = 4` (most restrictive) | ✅ |
| `unprivileged_bpf_disabled = 2` (BPF restricted) | ✅ |
| `ptrace_scope = 1` (restricted ptrace) | ✅ |
| No `/dev/vsock` exposed in sandbox | ✅ |
| WebFetch / WebSearch denied by policy engine | ✅ |
| Mounts are `nosuid,nodev` | ✅ |

---

## Fix Priority

```
1. [CRITICAL] Add seccomp profile + block CLONE_NEWUSER         → closes user ns escalation
2. [HIGH]     --tmpfs /opt/guestd in bwrap                      → hides host comm layer
3. [HIGH]     --tmpfs /opt/atelier in bwrap                     → hides runtime source
4. [HIGH]     kernel.kptr_restrict = 2 in sysctl               → hides kernel pointers
5. [MEDIUM]   Add seccomp to bwrap (--seccomp fd)               → reduces syscall surface
6. [MEDIUM]   Add spec_store_bypass_disable to kernel cmdline   → spectre/ssb mitigation
```

---

## Hardening Backlog

Forward-looking items ordered by priority. This section tracks what to build next.

### Critical

**C1 — Move the Anthropic credential out of the guest entirely.**
Terminate model calls at a host-side authenticated proxy. The guest should send model requests with
no ambient API key; the host injects a scoped credential. This closes the highest-value open finding
from the audit (CRIT-04): the key is visible in the guest process environment and memory. It also
reduces the impact of exfiltration attempts through the allowlisted Anthropic endpoint.

**C2 — Make the egress lock semantically tight, not just host-tight.**
The current allowlist blocks arbitrary DNS and direct-IP egress, but it still allows whatever
authenticated behavior the permitted endpoint exposes. After C1, put model traffic behind a narrow
proxy contract rather than letting the agent talk directly to the full provider API. Also close the
audit's port-80 finding and block link-local/RFC1918 metadata ranges.

**C3 — Drop root and capabilities.**
Status: **implemented and verified for agent execs.** guestd drops the child's real uid/gid to 1001
before execing bubblewrap, and bubblewrap drops all capabilities. Keep this as an invariant for
every non-operator execution path.

**C4 — Immutable rootfs and fixed system permissions.**
Status: **implemented and verified.** The root disk is read-only and image population now happens
in a Linux imager container so Unix ownership and modes survive. Continue to treat writable scratch
as explicit and narrow.

**C5 — Add seccomp.**
Apply a cBPF seccomp profile through bubblewrap. Docker's default profile is a reasonable floor;
then explicitly block dangerous syscalls such as `ptrace`, `process_vm_readv`, `process_vm_writev`,
`kexec_load`, module loading, raw BPF, and privileged mount operations where Node and the SDK do
not need them.

### High

**H1 — Make workspace writes reversible.**
Egress lockdown protects confidentiality, but it does not protect a user's files from destructive
edits inside the allowed workspace. Add snapshots, copy-on-write work areas, or explicit checkpoints
before risky operations.

**H2 — Gate the irreversible tail.**
The broker has the `Gate` seam and audit log, but the active implementation still uses `AllowAll`.
Replace it with real per-method/per-door policy. Keep routine work low-friction, but route
irreversible actions such as destructive deletes, publishing, pushing, sending, or broad egress
changes through an `Ask` or `Deny` decision.

**H3 — Add runtime backstops.**
Halt or escalate after repeated denied tool calls, unexpected egress attempts, or high-volume
file/network activity. This should be enforced outside the guest so a compromised process cannot
turn it off.

**H4 — Kill raw hardware and kernel surfaces.**
Enable kernel lockdown where compatible, restrict `/dev/mem`-style access, mount with `nodev` where
possible, and minimize the exposed device model.

**H5 — Add cgroup v2 resource limits.**
Set memory, CPU, and PID caps for agent children to contain fork bombs, runaway builds, and
accidental memory exhaustion.

**H6 — Hide or isolate foreign session mounts.**
The product now uses one shared VM with per-session shares under `/sessions/<id>`. That is
efficient, but it means session separation depends on the guest sandbox and mount permissions.
Follow up with one session per VM, per-session mount views, or read-only/hidden foreign mounts if
tenant-style isolation becomes a goal.

**H7 — Forward audit evidence off-guest.**
Broker audit exists on the host side. Add guest-side process/audit events only if they are
forwarded out of the VM, because a compromised guest can tamper with local-only logs.

### Medium

- Set `kernel.kptr_restrict=2`, `kernel.dmesg_restrict=1`, and
  `kernel.unprivileged_bpf_disabled=1`.
- Lock module loading after boot if no later dynamic module load is needed.
- Treat tool-readable content as untrusted. Files, web pages, repo text, and tool errors can all
  carry prompt injection.
- Avoid rendering attacker-controlled markdown images or remote links in privileged UI contexts.

---

## Architecture Notes

The vsock transport (`vmw_vsock_virtio_transport`) is loaded and active — this is the guestd ↔
host communication channel. The broker RPC protocol (`protocol.json`) exposes powerful operations
including `setEgressPolicy` and `attachWorkspace`. Ensuring the vsock channel is not reachable from
within the bwrap sandbox (no `/dev/vsock` exposed — ✅ already the case) is important to maintain.

The policy engine (`policy.ts`) correctly denies `WebFetch`/`WebSearch` and defaults to deny for
unknown tools. The `AllowAll` comment in the source (`"the broker's server-side gate remains
AllowAll today"`) indicates the host-side enforcement is not yet fully hardened — worth revisiting.

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
- Direct-by-IP TCP connections receive an instant RST
- All UDP (except DHCP) is silently dropped

---

## Assessment History (2026-05-22)

This section documents the original findings and the remediations applied, explaining how the
current posture was reached.

### Original Environment

| Property | Value |
|----------|-------|
| OS | Ubuntu 22.04.5 LTS (Jammy Jellyfish) |
| Kernel | 6.8.0-117-generic |
| Hypervisor | Microsoft Hyper-V (VMBus 5.3) |
| Architecture | x86_64 |
| Network Interface | tap0 — 192.168.127.2/24 |
| Network Transport | gvforwarder → vsock://2:1024 → HyperV host proxy |
| Init Process | /usr/sbin/guestd (custom, not systemd) |
| Running User | root (uid=0, gid=0) — **before remediation** |

### Original Finding Index

| ID | Severity | Title | Status |
|----|----------|-------|--------|
| CRIT-01 | Critical | Unconstrained root with all capabilities | ✅ Fixed & verified |
| CRIT-02 | Critical | No seccomp syscall filtering | ⏳ Deferred (bwrap can carry it) |
| CRIT-03 | Critical | Zero namespace isolation between processes | ✅ Substantially addressed |
| CRIT-04 | Critical | API key exposed in env vars & process memory | ⏳ Deferred — rotate now |
| CRIT-05 | Critical | World-writable system filesystem | ✅ Fixed & verified |
| HIGH-01 | High | No cgroup resource limits | ◻ Not yet addressed |
| HIGH-02 | High | No audit logging | ◻ Not yet addressed |
| HIGH-03 | High | SSH host keys world-readable/writable | ✅ Resolved (SSH removed) |
| HIGH-04 | High | /dev/mem and /dev/sda directly readable | ◻ Not yet addressed |
| HIGH-05 | High | Cross-session filesystem mount exposure | ◻ Not yet addressed |
| HIGH-06 | High | Port 80 open on vNIC filter alongside 443 | ⏳ Deferred |
| MED-01 | Medium | `kernel.kptr_restrict=0` | ◻ Not yet addressed |
| MED-02 | Medium | `kernel.modules_disabled=0` | ◻ Not yet addressed |
| MED-03 | Medium | ICMP spoofed locally for all destinations | ◻ Not yet addressed |

### What Was Fixed

**CRIT-05 — World-writable system filesystem.**
Root cause: the build extracted the rootfs and ran `mke2fs -d` on the Windows host, which cannot
preserve Unix owner/mode, so everything landed world-writable. The ext4 is now populated inside a
Linux "imager" container; only the opaque ext4 blob crosses to Windows. Sensitive perms are
normalized at build (`/usr`, `/etc` `0755 root:root`; `/etc/passwd` `0644`; `/etc/shadow` `0640`).
The root disk is additionally mounted read-only (`RootFSReadOnly` → SCSI `ReadOnly` + `ro` cmdline);
the few writable paths are tmpfs with explicit, non-world-writable modes (`/run`, `/sessions` `0755`;
`/home/atelier` `0700`; `/var/tmp` `1777`; `/tmp` per-sandbox `0755`).
*Verified in-guest:* `/usr/bin/bash` `0755 root:root`, `/etc/shadow` `0640`, `/` is `ro`, write to
`/usr/bin` → `EROFS`.

**CRIT-01 — Unconstrained root.**
The agent is launched inside bubblewrap as uid/gid **1001** with `--cap-drop ALL` and fresh
user/pid/ipc/uts namespaces (net deliberately shared so egress still works); the read-only root is
bind-mounted so system paths stay immutable. A subtle gap was caught during verification: with
guestd (PID 1, root) launching bwrap as root, bwrap's user namespace mapped sandbox-uid 1001 onto
host-uid-0 (`uid_map: 1001 0 1`) — so the agent reported uid 1001 but was DAC-root and could read
`/etc/shadow`. guestd now drops the child's real uid/gid to 1001 (`SysProcAttr.Credential`) before
exec'ing bwrap, so the namespace can only map to host-1001 and host-uid-0 files become foreign.
*Verified in-guest:* `id` → `uid=1001`; `CapEff/Prm/Bnd = 0`; `cat /etc/shadow` → Permission
denied; `/workspace`, `/home/atelier`, `/tmp` writable; `/run`, `/sessions` denied; a real agent
run completed end-to-end.

**CRIT-03 — Namespace isolation — substantially addressed for the agent.**
bubblewrap gives the agent its own user, pid, ipc, uts, and mount namespaces. The network namespace
is intentionally shared (the egress jail is the boundary). The trusted control-plane processes
(guestd, gvforwarder) still share namespaces among themselves.

**HIGH-03 — SSH host keys — resolved.**
`openssh-server` was removed from the image (an unused listener), so there are no host keys to
expose.

**Networking change.**
DHCP was removed (`isc-dhcp-client` dropped). The guest now configures `tap0` statically
(192.168.127.2, gw .1, fixed MAC) and runs gvforwarder with `-preexisting`. The host-side
DNS-sinkhole + TCP allowlist is unchanged; egress allow/deny was re-verified (`api.anthropic.com`
reachable; other hosts `NXDOMAIN`).

### Still Open from Original Assessment

- **CRIT-04** — API key still injected via env var. **Rotate the key now** (it was exposed during
  the assessment) and move to a tmpfs file / short-lived scoped key.
- **CRIT-02** — No seccomp yet; bubblewrap can carry a cBPF profile via `--seccomp` next.
- **HIGH-06** — Port 80 still reachable post-DNS; gate the allowlist to 443.
- HIGH-01/02/04/05, MED-01/02/03 — not yet addressed.

### Original Finding Detail

#### CRIT-01 — Process Running as Unconstrained Root ✅ Fixed

The Claude agent and all child processes ran as `uid=0 gid=0 (root)` with all 41 Linux
capabilities granted and no restrictions.

| Capability | Risk |
|------------|------|
| `CAP_SYS_MODULE` | Load arbitrary kernel modules (rootkit insertion) |
| `CAP_SYS_ADMIN` | Mount filesystems, manipulate namespaces, bypass many controls |
| `CAP_SYS_PTRACE` | Read/write memory of any process |
| `CAP_SYS_RAWIO` | Direct hardware access (disk, memory ports) |
| `CAP_SYS_BOOT` | Reboot or kexec a new kernel |
| `CAP_MAC_OVERRIDE` | Bypass AppArmor/SELinux |
| `CAP_NET_ADMIN` | Reconfigure network interfaces, routing, firewall rules |
| `CAP_DAC_OVERRIDE` | Bypass all filesystem permission checks |

**Remediation applied:** bubblewrap launch as uid/gid 1001, `--cap-drop ALL`, guestd drops child
uid before exec.

#### CRIT-02 — No Seccomp Filtering ⏳ Deferred

No seccomp profile applied; all ~350 Linux syscalls available. Combined with full root capabilities,
dangerous syscalls (`ptrace`, `process_vm_readv`, `kexec_load`, `init_module`) were freely callable.

**Remediation:** Apply Docker's default seccomp profile as a minimum baseline via
`bwrap --seccomp <fd>`.

#### CRIT-03 — Zero Namespace Isolation ✅ Substantially addressed

Every process shared all namespaces with PID 1. Any process could observe and interact with every
other process's filesystem view, network stack, IPC objects, and process tree without any
kernel-enforced boundary.

**Remediation applied:** bwrap provides agent with dedicated user/pid/ipc/uts/mnt namespaces.

#### CRIT-04 — API Key Exposed in Environment Variables ⏳ Deferred

`ANTHROPIC_API_KEY` was readable from `/proc/PID/environ` for every process. Found in **73
distinct memory regions** across all running processes — heap, stack, and anonymous mappings of
the node runtime, the agent binary, and PID 1 (guestd).

**Remediation:** Mount secrets via a tmpfs file readable only by the agent user. Use per-session
ephemeral keys revoked when the VM terminates. **Rotate the key immediately.**

#### CRIT-05 — World-Writable System Filesystem ✅ Fixed

System directories and binaries were world-writable (`/usr/bin`, `/etc`, `/etc/passwd`,
`/etc/shadow`, shell + node binaries). Root cause: mke2fs on Windows host doesn't preserve Unix
permissions.

**Remediation applied:** ext4 populated inside Linux imager container; root disk mounted
read-only; writable paths are tmpfs with explicit modes.

#### HIGH-01 — No Cgroup Resource Limits ◻ Open

No memory or CPU limits applied via cgroups. A runaway workload can exhaust all VM memory or
consume 100% CPU indefinitely.

**Remediation:** Set `memory.max` and `cpu.max` in the agent's cgroup slice (e.g. 2 GB memory,
150% CPU).

#### HIGH-02 — No Audit Logging ◻ Open

`auditd` is not running. No syscall-level audit trail, no process execution logging, no file
access recording.

**Remediation:** Enable auditd with rules covering `execve`, `open`/`openat` on sensitive paths,
`ptrace`, network connect, module load. Forward logs to the host via vsock before the VM can
tamper with them.

#### HIGH-04 — Raw Disk and Physical Memory Accessible ◻ Open

Both `/dev/sda` (raw block device) and `/dev/mem` (physical memory) were present and readable.
With `CAP_SYS_RAWIO` (now dropped by bwrap), this is lower severity than before, but the devices
should still be absent.

**Remediation:** Bind-mount `/dev/null` over `/dev/mem`; remove `/dev/sda` from the VM's `/dev`
if direct disk access is not required.

#### HIGH-05 — Cross-Session Filesystem Mount Exposure ◻ Open

Three separate session workspaces were mounted inside a single VM via 9p/VirtioFS, all
`rw,relatime` with no access restrictions. Each VM should receive only its own session mount.

#### HIGH-06 — Port 80 Open on vNIC Filter ⏳ Deferred

After resolving `api.anthropic.com` via the gateway DNS, TCP port 80 is reachable as well as 443.
Port 80 serves no purpose for an HTTPS API.

**Remediation:** Restrict the TCP allowlist to port 443 only.

#### MED-01 — `kptr_restrict = 0` ◻ Open

Kernel symbol addresses visible in `/proc/kallsyms`. Aids exploit development by defeating KASLR.

**Remediation:** `sysctl -w kernel.kptr_restrict=2`

#### MED-02 — `kernel.modules_disabled = 0` ◻ Open

New kernel modules can be loaded at runtime. With `CAP_SYS_MODULE` (now dropped for the agent),
this risk is reduced but not eliminated for control-plane processes.

**Remediation:** `sysctl -w kernel.modules_disabled=1` (post-boot, one-way).

#### MED-03 — ICMP Spoofed Locally ◻ Open

`gvforwarder` generates fake ICMP echo replies for all destinations without forwarding packets.
Ping always succeeds even for completely unreachable hosts, misleading operators and masking
network partitions.

**Remediation:** Drop ICMP outright, or forward honestly.

### Host-Side Issues Found During Verification

- **broker `stopVM` reported a phantom HCS error** (`HcsCloseComputeSystem hresult=0x8f3a51f0`):
  the binding interpreted a return value from `HcsCloseComputeSystem`, which is documented `void`.
  **Fixed** (binding no longer reads the return).
- **broker can panic on VM teardown** (`linuxkit/virtsock` hvsock `ioCompletionProcessor` —
  "The handle is invalid") when a VM is stopped while its egress connection is mid-handshake.
  Pre-existing third-party-library race; **open**.

---

## Source References

- Anthropic, "Making Claude Code more secure and autonomous":
  https://www.anthropic.com/engineering/claude-code-sandboxing
- Anthropic, "Claude Code auto mode":
  https://www.anthropic.com/engineering/claude-code-auto-mode
- Anthropic, `anthropic-experimental/sandbox-runtime`:
  https://github.com/anthropic-experimental/sandbox-runtime
- OpenAI Codex, agent approvals and security:
  https://developers.openai.com/codex/agent-approvals-security
- Pluto Security, "Inside Claude Cowork":
  https://pluto.security/blog/inside-claude-cowork-how-anthropics-autonomous-agent-actually-works/
- Simon Willison, "The lethal trifecta for AI agents":
  https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/
- OWASP Top 10 for LLM Applications:
  https://genai.owasp.org/llm-top-10/
- NIST AI RMF Generative AI Profile:
  https://www.nist.gov/publications/artificial-intelligence-risk-management-framework-generative-artificial-intelligence
- Firecracker production host setup:
  https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md
- CIS Benchmarks:
  https://www.cisecurity.org/cis-benchmarks

---

*Classification: Internal / Security Sensitive*
