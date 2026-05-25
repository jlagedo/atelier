# Live Security Assessment — Guest VM Sandbox

**Date:** 2026-05-24
**Kernel:** 6.17.0-29-generic
**OS:** Ubuntu 24.04.4 LTS (aarch64)
**Hypervisor:** Apple Silicon VZ VM
**Assessor:** Claude (sandboxed agent, uid=1001)

---

## TL;DR

The **isolation spine is solid**: read-only rootfs, dropped capabilities, `NoNewPrivs`, seccomp filter,
user/PID/IPC/UTS/MNT namespacing via bwrap, and no SUID/SGID binaries — and, as of 2026-05-25, a
**curated read-only mount allow-list** (no whole-rootfs bind), **per-exec cgroup limits**, **kernel
sysctl hardening + module-load latch**, and a **self-applied Landlock LSM domain** (FS allow-list +
outbound TCP 443 only). The **most dangerous open issue is F-02** — the Anthropic API key is live in the
process environment right now, readable by any process running as uid 1001.

**Open finding counts:** 1 critical · 6 high · 4 medium. (5 fixed 2026-05-25: F-03, F-04, F-06, F-09,
F-16 — commit `7193d79`, validated on a real VZ boot, 43/43 e2e.)

---

## Environment Fingerprint

| Property | Value |
|---|---|
| Hostname | `(none)` (UTS namespace, no hostname set) |
| Kernel | `Linux 6.17.0-29-generic #29~24.04.1-Ubuntu SMP PREEMPT_DYNAMIC` |
| Architecture | `aarch64` |
| OS | Ubuntu 24.04.4 LTS (Noble Numbat) |
| Agent user | `atelier` uid=1001 gid=1001, no supplementary groups |
| PID 1 | `/usr/bin/bwrap` (bubblewrap sandbox) |
| Agent PID | Node.js tsx process tree (pids 2, 13, 25, 30) |
| Network | `tap0` at `192.168.127.2/24`, gw `192.168.127.1`; loopback down |
| Session workspace | `/sessions/s6f5e4f8d8d` (virtiofs mount, rw) |
| Session ID | `5d29c1c9-e321-4218-891a-881d45ce2c12` |

### bwrap invocation (from `/proc/1/cmdline`)

```
/usr/bin/bwrap
  --unshare-user --unshare-pid --unshare-ipc --unshare-uts
  --uid 1001 --gid 1001
  --cap-drop ALL
  --new-session
  --die-with-parent
  --bind / /
  --dev /dev --proc /proc --tmpfs /tmp
  --seccomp 3
  -- /opt/atelier/packages/artisan/node_modules/.bin/tsx src/cli-guest.ts
     --serve --workspace /sessions/s6f5e4f8d8d
```

---

## Working Controls (verified this session)

| Control | Evidence |
|---|---|
| Running as uid/gid **1001** — not root | `id → uid=1001(atelier)` |
| **All capabilities dropped** | `CapInh/CapPrm/CapEff/CapBnd/CapAmb = 0x0000000000000000` |
| `NoNewPrivs = 1` | `/proc/self/status` |
| **Seccomp mode 2** (cBPF filter active) | `Seccomp: 2`, `Seccomp_filters: 1` |
| Root filesystem **read-only** | `touch /test_write → EROFS`; mountinfo shows `ro` |
| Separate user/pid/ipc/uts/mnt **namespaces** via bwrap | `/proc/self/ns/*` all distinct |
| **No SUID/SGID binaries** | `find / -perm -4000` and `-2000` returned nothing |
| `sudo` not available | `sudo -l → not available/permitted` |
| `dmesg_restrict = 1` | `/proc/sys/kernel/dmesg_restrict` |
| `perf_event_paranoid = 4` | `/proc/sys/kernel/perf_event_paranoid` |
| `unprivileged_bpf_disabled = 2` | `/proc/sys/kernel/unprivileged_bpf_disabled` |
| `ptrace_scope = 1` | `/proc/sys/kernel/yama/ptrace_scope` |
| No `/dev/vsock` in sandbox | `/dev` listing confirmed absent |
| Only loopback + `tap0`; no listening TCP ports | `ip addr`, `/proc/net/tcp` |
| Minimal `/dev` (null, zero, full, random, urandom, tty, pts only) | `ls /dev` |
| WebFetch / WebSearch denied by guest policy engine | `~/.claude/policy-limits.json` |
| Mounts carry `nosuid,nodev` | `/proc/self/mountinfo` |
| **Curated read-only mount allow-list** (no whole-rootfs bind) — *2026-05-25* | bwrap `--ro-bind /usr,/opt/atelier,/etc`-subset; `/opt/runner` + sibling sessions + raw disk/vsock absent (F-03/F-09) |
| **Per-exec cgroup v2 limits** — *2026-05-25* | `pids.max=512`, `memory.max=2G`, `swap=0`, `cpu.max=2c` via `CgroupFD` (F-06) |
| **Kernel sysctl hardening + module latch** — *2026-05-25* | `kptr_restrict=2`, `io_uring_disabled=2`, `ptrace_scope=2`, `modules_disabled=1` (F-04/F-16) |
| **Landlock LSM domain** (self-applied) — *2026-05-25* | FS allow-list + outbound TCP 443 only; denies out-of-policy reads and non-443 connects |

---

## Critical Findings

### F-02 · Anthropic API key resident in guest environment and memory

- **Severity:** Critical
- **Status:** Open (deferred)
- **First seen:** 2026-05-22
- **Confirmed live:** 2026-05-24 (this session)

**Description.**
`ANTHROPIC_API_KEY` is injected into the in-guest process environment and is readable from
`/proc/PID/environ` by any process running as uid 1001 — which includes every tool call the agent
makes. The key is also resident in heap, stack, and anonymous memory mappings of the Node runtime and
agent process across the full session lifetime.

**Live evidence.** Key prefix `sk-ant-api03-bpGK…` observed in `env` output during assessment.
Present in environment of pid 1 (bwrap), pid 2 and 13 (Node agent), and all child shells.

**Impact.** Highest-value open finding: enables API key theft via prompt injection, exfiltration
through the allowlisted `api.anthropic.com` endpoint, or any future sandbox escape. Combines with
F-03 (policy source readable) to give an attacker both the key and the full policy surface.

**Recommendation.**
1. **Rotate the exposed key immediately** — it was observed and recorded during this session.
2. **Architectural fix:** terminate model calls at a **host-side authenticated proxy**. The guest
   sends requests with no ambient key; the host injects a scoped, per-session ephemeral credential
   that is revoked on VM teardown.
3. **Interim:** mount the secret as a `tmpfs` file readable only by the agent user rather than
   setting it as an environment variable — at minimum this removes it from `/proc/PID/environ`.

---

## High Findings

### F-03 · `/opt` runner volume fully readable inside the sandbox

- **Severity:** High
- **Status:** **Fixed** — 2026-05-25 (commit `7193d79`)
- **First seen:** 2026-05-24
- **Resolution.** bwrap no longer does `--bind / /`. It mounts a curated read-only toolbox
  (`/usr`, `/opt/atelier`, a small `/etc` allow-list) plus the agent's own workspace, so the
  runner volume at `/opt/runner` (ELF + seccomp blob) is never mounted in the first place. A
  self-applied Landlock domain denies it a second time. Verified by e2e: `cat
  /opt/runner/atelier-runner` fails inside the sandbox while `/opt/atelier` stays usable.

**Description.**
`--bind / /` in the bwrap invocation exposes all host mounts, including the read-only runner volume
at `/opt`. Both the host-comms binary and the complete in-guest agent source tree are readable:

```
/opt/runner/atelier-runner                                    ← host-comms Go ELF
/opt/atelier/packages/artisan/src/seams/policy.ts       ← full allow/deny policy engine
/opt/atelier/packages/artisan/src/broker/client.ts      ← broker client + RPC schema
/opt/atelier/packages/artisan/src/cli-guest.ts          ← guest entrypoint
```

An in-sandbox attacker can enumerate the host-comms layer and read the full policy engine and
canonical RPC schema (including `setEgressPolicy`) to aid targeted exploitation.

**Recommendation.** Add `--tmpfs /opt` *after* `--bind / /` in the bwrap arguments (bwrap applies
bind args in order; a later entry shadows the earlier path for that mountpoint).

---

### F-04 · `kptr_restrict = 0` — kernel pointer restriction disabled

- **Severity:** High
- **Status:** **Fixed** — 2026-05-25 (commit `7193d79`)
- **First seen:** 2026-05-24
- **Resolution.** `image/guest/init.sh` now sets `kernel.kptr_restrict = 2` at boot (along
  with `yama.ptrace_scope = 2`). Verified by e2e (sysctl read-back shows `kptr=2`).

**Description.**
`/proc/sys/kernel/kptr_restrict` = **0**. Kernel pointers in `/proc/kallsyms` and related interfaces
are unrestricted, aiding exploit development by defeating KASLR. `/proc/kallsyms` currently returns
all-zero addresses inside the user namespace, but the setting may still leak pointers via `/proc/net`,
dmesg, etc.

**Recommendation.** Set `kernel.kptr_restrict = 2` (hide from all users including root) in sysctl.

---

### F-05 · Egress allowlist permits TCP port 80

- **Severity:** High
- **Status:** Open (deferred)
- **First seen:** 2026-05-22

**Description.**
TCP port 80 is reachable on the allowed egress host alongside 443. Port 80 serves no legitimate
purpose for an HTTPS-only API. Additionally, the agent talks directly to the full provider API rather
than through a narrow host-side contract.

**Live evidence.** Two established connections on port 443 observed in `/proc/net/tcp`; port 80
is also on the allowlist per prior assessment.

**Recommendation.** Restrict the TCP allowlist to **443 only**; block link-local and RFC1918 metadata
address ranges. After F-02 is addressed, route all model traffic through a narrow host-side proxy.

---

### F-06 · No cgroup resource limits

- **Severity:** High
- **Status:** **Fixed** — 2026-05-25 (commit `7193d79`)
- **First seen:** 2026-05-22
- **Resolution.** init.sh mounts cgroup v2 and delegates controllers; runner places each
  sandboxed exec in its own cgroup via `SysProcAttr.CgroupFD` with `pids.max=512`,
  `memory.max=2G`, `memory.swap.max=0`, `cpu.max="200000 100000"` (2 cores). Soft-fails to
  unlimited if cgroup2 is unavailable (the bwrap+seccomp boundary still holds). Verified by
  e2e: a fork probe is capped (~509 of 700 spawned).

**Description.**
No `memory.max`, `cpu.max`, or `pids.max` limits are applied to agent children. A runaway workload
(fork bomb, runaway build, memory exhaustion) can consume all VM resources indefinitely.

**Recommendation.** Set cgroup v2 limits on the agent's slice (e.g. `memory.max=2G`, `cpu.max=150%`,
`pids.max=512`).

---

### F-07 · No audit logging / no off-guest evidence forwarding

- **Severity:** High
- **Status:** Open
- **First seen:** 2026-05-22

**Description.**
`auditd` is not running: no syscall-level trail, process-exec logging, or file-access recording
inside the guest. Broker audit exists host-side, but a compromised guest can tamper with any
local-only log.

**Recommendation.** Enable auditd with rules covering `execve`, `openat` on sensitive paths,
`ptrace`, network connect, and module load — and **forward records off-guest via vsock** before the
VM has a chance to tamper with them.

---

### F-09 · Cross-session filesystem mount exposure

- **Severity:** High
- **Status:** **Fixed** — 2026-05-25 (commit `7193d79`)
- **First seen:** 2026-05-22
- **Resolution.** The narrowed bind mounts only the exec's own workspace (derived from the
  agent's `--workspace` arg), never the `/sessions` parent, so sibling sessions are absent
  from the namespace entirely. Verified by e2e (an s1-scoped exec cannot see or read s2).
- **Note.** This is namespace-level isolation on one shared kernel — appropriate for the
  single-user desktop model (all sessions belong to the same user). True multi-tenant
  isolation would still require one VM per session; explicitly out of scope here.

**Description.**
The product uses one shared VM with per-session shares under `/sessions/<id>`. Session separation
therefore depends on guest sandbox permissions and mount ACLs rather than a hypervisor boundary;
foreign session mounts may be observable inside the VM.

**Recommendation.** If tenant-style isolation becomes a requirement, move to one session per VM,
per-session mount views, or hidden/read-only foreign mounts.

---

### F-10 · Broker policy gate is `AllowAll` — irreversible tail ungated

- **Severity:** High
- **Status:** Open
- **First seen:** 2026-05-22

**Description.**
The broker has a `Gate` seam and audit log, but the active implementation is `AllowAll`, so
host-side enforcement is not yet real per-method/per-door policy. The guest-side `policy.ts`
correctly denies `WebFetch`/`WebSearch` and defaults-deny unknown tools, but the host gate is the
durable boundary and it is currently inactive.

**Recommendation.** Replace `AllowAll` with real per-method policy. Route irreversible actions
(destructive deletes, publishing, pushing, sending, broad egress changes) through an `Ask` or `Deny`
decision enforced host-side.

---

### F-11 · Workspace writes are not reversible

- **Severity:** High
- **Status:** Open
- **First seen:** 2026-05-22

**Description.**
Egress lockdown protects confidentiality but does not protect the user's files from destructive edits
inside the allowed workspace. There is no snapshot, copy-on-write work area, or checkpoint mechanism.

**Recommendation.** Add snapshots or explicit checkpoints before risky operations; consider a
copy-on-write work area that is only merged on explicit user approval.

---

### F-12 · No runtime backstops for anomalous behavior

- **Severity:** High
- **Status:** Open
- **First seen:** 2026-05-22

**Description.**
There is no mechanism to halt or escalate on repeated denied tool calls, unexpected egress attempts,
or high-volume file/network activity. Any such mechanism must be enforced *outside* the guest process
to be tamper-proof.

**Recommendation.** Add host-side backstops that halt or escalate when anomalous signals are
observed; do not rely on in-guest instrumentation.

---

## Medium Findings

### F-14 · Speculation Store Bypass not mitigated

- **Severity:** Medium
- **Status:** Open
- **First seen:** 2026-05-24

**Live evidence.**
```
Speculation_Store_Bypass: vulnerable
SpeculationIndirectBranch: unknown
```
(from `/proc/self/status`)

Lower risk on Apple Silicon than x86 but relevant for multi-tenant scenarios.

**Recommendation.** Add `spec_store_bypass_disable=seccomp` (or `=on`) to the kernel cmdline.
Current cmdline: `console=hvc0 root=/dev/vda ro noresume init=/sbin/init`.

---

### F-15 · Sensitive config readable by agent

- **Severity:** Medium
- **Status:** Open
- **First seen:** 2026-05-24

**Description.**
`~/.claude/policy-limits.json` (the full restriction config, including which capabilities are
disabled) is owned by `atelier` and is readable by the agent. The policy surface is fully visible
to the sandboxed process.

**Recommendation.** Move policy-limits out of the agent-readable path, or encrypt the sensitive
fields. If the agent genuinely needs these values, scope the exposure to the minimum needed.

---

### F-16 · Kernel module loading enabled at runtime

- **Severity:** Medium
- **Status:** **Fixed** — 2026-05-25 (commit `7193d79`)
- **First seen:** 2026-05-22
- **Resolution.** init.sh sets `kernel.modules_disabled = 1` (one-way latch) just before
  exec'ing runner — after preloading every module the guest needs (vsock, virtiofs, the 9p
  stack, `tun`, `loop`; ext4 via initramfs). Verified by e2e (sysctl read-back shows
  `modules=1`).

**Live evidence.** `/proc/sys/kernel/modules_disabled` = **0**.

**Description.**
New kernel modules can be loaded at runtime. With `CAP_SYS_MODULE` dropped for the agent the direct
risk is reduced, but control-plane processes (runner) retain this surface.

**Recommendation.** Set `kernel.modules_disabled = 1` post-boot (one-way latch) once no later
dynamic module load is required; enable kernel lockdown where compatible.

---

### F-17 · ICMP spoofed locally for all destinations

- **Severity:** Medium
- **Status:** Open
- **First seen:** 2026-05-22

**Description.**
`gvforwarder` generates fake ICMP echo replies for all destinations without forwarding packets, so
`ping` always succeeds — even for unreachable hosts — masking network partitions and misleading
operators about actual reachability.

**Recommendation.** Drop ICMP outright at the sandbox boundary, or forward it honestly.

---

### F-18 · Tool-readable content is untrusted (prompt injection)

- **Severity:** Medium
- **Status:** Open
- **First seen:** 2026-05-22

**Description.**
Files, web pages, repo text, and tool error messages can all carry prompt-injection payloads.
Rendering attacker-controlled markdown (including images or remote links) in a privileged UI context
compounds the risk. This finding combines with F-02: a successful injection could exfiltrate the
resident API key through the allowlisted egress endpoint.

**Recommendation.** Treat all tool-readable content as untrusted input; avoid rendering
attacker-controlled markdown images or remote links in privileged UI contexts; consider a
content-integrity layer for high-risk operations.

---

## Resolved Findings (for reference)

| ID | Summary | Status |
|---|---|---|
| R-01 | Agent ran as unconstrained root with all capabilities | **Fixed** — now uid=1001, `--cap-drop ALL` |
| R-02 | World-writable system filesystem | **Fixed** — rootfs read-only, writable paths are tmpfs |
| R-03 | Zero namespace isolation between processes | **Fixed** — bwrap user/pid/ipc/uts/mnt namespaces |
| R-04 | SSH host keys world-readable | **Fixed** — openssh-server removed from image |
| R-05 | DHCP client present | **Applied** — static network config, isc-dhcp-client dropped |
| F-13 | No seccomp filter | **Fixed** — cBPF Docker-default profile via `bwrap --seccomp`; `unshare(CLONE_NEWUSER)` returns `EPERM` |
| F-01 | Unprivileged user-namespace creation grants full caps | **Fixed** — closed by F-13 seccomp profile |
| F-03 | `/opt` runner volume readable in sandbox | **Fixed** — narrowed bwrap bind (curated ro toolbox); `/opt/runner` not mounted; Landlock denies it |
| F-04 | `kptr_restrict = 0` | **Fixed** — `kernel.kptr_restrict = 2` set in init.sh at boot |
| F-06 | No cgroup resource limits | **Fixed** — per-exec cgroup v2 (`pids.max`/`memory.max`/`swap`/`cpu.max`) via `CgroupFD` |
| F-09 | Cross-session filesystem mount exposure | **Fixed** — narrowed bind exposes only the exec's own workspace, never `/sessions` parent |
| F-16 | Kernel module loading enabled at runtime | **Fixed** — `kernel.modules_disabled = 1` latch after module preload |

---

## Package Install Probe (2026-05-24)

Package registry access is **intentionally open** in this configuration. The following was confirmed
live and is expected behaviour, not a defect:

| Host | DNS | TCP 443 | TCP 80 | Notes |
|---|---|---|---|---|
| `api.anthropic.com` | ✅ | ✅ | ✅ | Model API (port 80 still subject to F-05) |
| `pypi.org` | ✅ | ✅ | ✅ | Intentionally open |
| `files.pythonhosted.org` | ✅ | ✅ | — | Intentionally open |
| `registry.npmjs.org` | ✅ | ✅ | — | Intentionally open |
| `www.google.com` | ❌ | — | — | Correctly blocked |
| `github.com` | ❌ | — | — | Correctly blocked |
| `raw.githubusercontent.com` | ❌ | — | — | Correctly blocked |

`pip install requests` succeeded; packages land in `~/.local/lib/python3.12/site-packages/`
(user site-packages on the writable `/home/atelier` tmpfs).

### Residual risks to note (given that registry access is open by design)

Even as an intentional capability, the following are worth tracking:

- **Supply-chain × prompt injection:** a prompt-injection payload (F-18) could cause the agent to
  `pip install` a malicious package, which then runs arbitrary code as uid 1001 and can read the
  API key from the environment (F-02). The combination F-18 + open registries + F-02 is the highest
  practical exploitation chain in the current posture.
- **Port 80 on `pypi.org`:** same class as F-05 — no legitimate use for HTTPS-only traffic.
  Recommend restricting the registry allowlist to port 443 only.
- **User site-packages persist for the session:** packages installed mid-session are immediately
  importable by all subsequent Python calls. This is acceptable for a tmpfs-backed home that is
  discarded on VM teardown, but worth confirming teardown is complete and not reused across sessions.

---

## Remediation Priority

Ordered by leverage:

1. **Rotate the API key** — it was observed and recorded during this session. Do this now.
2. **F-02** — host-side credential proxy; per-session ephemeral keys. Closes the highest-value data finding.
3. ✅ **F-03 — done (2026-05-25).** Narrowed the bwrap bind to a curated read-only toolbox (stronger than the original `--tmpfs /opt` idea, which would have hidden the agent's own code too); `/opt/runner` is no longer mounted, and Landlock denies it as well.
4. ✅ **F-04 — done (2026-05-25).** `kernel.kptr_restrict = 2` set at boot.
5. **F-05** — restrict egress TCP to port 443 on all allowlisted hosts (including pypi.org); block metadata address ranges.
6. ✅ **F-06 — done (2026-05-25).** Per-exec cgroup v2 `memory.max`, `cpu.max`, `pids.max`, `memory.swap.max=0`.
7. **F-10** — replace broker `AllowAll` gate with real per-method policy.
8. **F-14** — `spec_store_bypass_disable=seccomp` on kernel cmdline (still open). ✅ **F-16 done (2026-05-25)** — `modules_disabled=1` latched after module preload.
   **New layer added:** a self-applied **Landlock LSM** domain now confines the agent's filesystem + outbound TCP (443 only), independent of bwrap.
9. **F-07** — auditd with off-guest vsock forwarding.
10. **F-11 / F-12** — workspace snapshots; host-side anomaly backstops.

---

## Architecture Notes

The vsock transport (`vmw_vsock_virtio_transport`) is the runner ↔ host channel. The broker RPC
protocol exposes powerful operations including `setEgressPolicy` and `attachWorkspace`; keeping the
vsock channel unreachable from inside the bwrap sandbox (no `/dev/vsock` exposed — ✅) is a critical
invariant to maintain.

All VM network traffic flows through a user-space proxy chain:

```
VM (tap0) ──► gvforwarder (user-space, /dev/net/tun)
           ──► vsock://2:1024 (HyperV VMBus / VZ)
           ──► Host-side proxy (enforces allowlist policy)
           ──► Internet
```

The host-side proxy implements a **DNS-sinkhole + TCP allowlist** pattern:
- Only `api.anthropic.com` resolves (all others return `NXDOMAIN`).
- TCP connections are only permitted after a DNS lookup for an allowed hostname.
- Direct-by-IP TCP receives an instant RST.
- All UDP (except DHCP) is silently dropped.

The policy engine (`policy.ts`) denies `WebFetch`/`WebSearch` and defaults-deny for unknown tools.
The `AllowAll` host-side gate (F-10) is the remaining gap in this chain.

---

*Classification: Internal / Security Sensitive*
