# VM Sandbox Security

Scope: securing the **guest VM as the containment boundary** for an autonomous Claude agent
that works on local files. The App-to-Broker IPC boundary is covered separately in
[`ipc-security.md`](ipc-security.md).

This document is the single register of security findings for the guest sandbox. Each issue —
whether open, deferred, or fixed — appears **once**, with a stable ID, severity, status, evidence,
and recommendation. Prior per-assessment IDs (`CRIT-*`, `HIGH-*`, `MED-*`, the 2026-05-24 numbered
items, and the `C*`/`H*` backlog labels) are listed under **Prior refs** for traceability.

> Severity/status reflect what was recorded by the assessments below; this revision only
> consolidates and reformats — it does not re-verify. Where two assessments disagreed, the most
> recent value is the headline and the older one is noted.

---

## Assessment Timeline

| Date | Environment | Findings | Notes |
|------|-------------|----------|-------|
| 2026-05-22 | Ubuntu 22.04.5 LTS · Linux 6.8.0 · x86_64 · Hyper-V | 6 critical, 6 high, 3 medium | Initial assessment |
| 2026-05-22 | (same) | — | Remediations applied and verified on live HCS boot |
| 2026-05-24 | Ubuntu 24.04.4 LTS · Linux 6.17.0 · aarch64 · Apple Silicon VZ VM | 1 critical, 3 high, 3 medium | Follow-up post-remediation |
| 2026-05-24 | (same) | — | Seccomp profile added → F-01 + F-13 remediated |

**Assessor:** Claude (sandboxed agent)

---

## Current Posture

The isolation spine is strong: a dedicated Linux utility VM, a default-deny host-mediated egress
path, a read-only root disk, and bubblewrap for the agent process. Remaining risk is concentrated in
key residency, syscall filtering, resource limits, and shared-VM session separation.

| Area | Current state | Code reference | Finding |
|---|---|---|---|
| Hypervisor boundary | Dedicated Linux utility VM driven by HCS/VZ | `services/internal/hcs`, `services/internal/vmm` | — |
| Agent identity | Launched as uid/gid 1001, not root | `services/cmd/guestd/sandbox_linux.go` | R-01 |
| Agent process sandbox | bwrap user/pid/ipc/uts/mnt namespaces; caps dropped | `services/cmd/guestd/sandbox_linux.go` | R-01, R-03 |
| Root filesystem | rootfs read-only; writable paths are tmpfs/session shares | `services/internal/hcs/doc.go`, `image/guest/init.sh` | R-02 |
| Egress | runtime hostname allowlist + DNS pinning; default deny | `services/internal/netjail` | F-05 |
| Model credential | still passed into the in-guest process environment | `apps/desktop/src/main/sessions/manager.ts`, `packages/agent/src/cli-guest.ts` | F-02 |
| Seccomp | cBPF profile applied via `bwrap --seccomp` (Docker default, no-cap) | `services/cmd/guestd/sandbox_linux.go`, `image/agent/seccomp` | F-13 |
| Resource limits | no cgroup limits yet | — | F-06 |

**Open finding counts (headline severity):** 1 critical · 10 high · 5 medium.

---

## Open Findings

### Critical

#### F-02 · Anthropic API key resident in guest environment and memory
- **Severity:** Critical · **Status:** Open (deferred) · **First seen:** 2026-05-22 · **Prior refs:** CRIT-04, backlog C1

**Description.** `ANTHROPIC_API_KEY` is injected into the in-guest process environment, readable from
`/proc/PID/environ`, and was found in **73 distinct memory regions** across processes (heap, stack,
anon mappings of the node runtime, the agent, and PID 1). Highest-value open finding: it enables key
theft and increases the impact of exfiltration through the allowlisted endpoint.

**Evidence.** Key present in `manager.ts` launch env → `cli-guest.ts` process; visible via
`/proc/PID/environ` and memory scan.

**Recommendation.** Terminate model calls at a **host-side authenticated proxy**: the guest sends
requests with no ambient key and the host injects a scoped credential (backlog C1). Interim: mount
the secret via a tmpfs file readable only by the agent user; use per-session ephemeral keys revoked
on VM teardown. **Rotate the exposed key now** (it was observed during assessment).

### High

#### F-03 · guestd volume contents readable inside the sandbox
- **Severity:** High · **Status:** Open · **First seen:** 2026-05-24 · **Prior refs:** 2026-05-24 #2, #3

**Description.** `--bind / /` in the bwrap invocation exposes all host mounts, including the
read-only guestd volume mounted at `/opt`. Both the `guestd` Go binary (`/opt/guestd/guestd`) and the
entire in-guest agent source tree (`/opt/atelier`) are fully readable. This lets an in-sandbox
attacker enumerate the host-comms layer and read the complete policy engine, broker client, and
canonical RPC schema to aid targeted exploitation:
```
/opt/guestd/guestd                                   ← host comms binary (4 MB Go ELF)
/opt/atelier/packages/agent/src/seams/policy.ts      ← full policy engine (allow/deny sets)
/opt/atelier/packages/agent/src/broker/client.ts     ← broker client
/opt/atelier/packages/agent/src/cli-guest.ts         ← guest entrypoint
/opt/atelier/packages/protocol/schema/protocol.json  ← full RPC schema (incl. setEgressPolicy)
```

**Evidence.** `cat /proc/mounts` shows `/dev/vdb /opt ext4 ro,nosuid,nodev`; the paths above are
readable, including from within a user namespace.

**Recommendation.** Shadow the mount with `--tmpfs /opt` placed **after** `--bind / /` (bwrap
applies bind args in order; a later entry shadows the earlier path for that mount). Because guestd
and the agent now share the single `/opt` volume, one `--tmpfs /opt` covers both (this previously
required separate `/opt/guestd` and `/opt/atelier` shadows). Verify with `cat /proc/mounts` in a
fresh session.

#### F-04 · `kptr_restrict = 0` — kernel pointer restriction disabled
- **Severity:** High · **Status:** Open · **First seen:** 2026-05-24 · **Prior refs:** 2026-05-24 #4, MED-01 (logged Medium on 2026-05-22)

**Description.** Kernel pointers in `/proc/kallsyms` and similar interfaces are not restricted, aiding
exploit development by defeating KASLR. `/proc/kallsyms` currently returns all-zero addresses (kernel
ASLR + userns mapping), but the setting may still leak pointers via `/proc/net`, dmesg, etc.

**Evidence.** `cat /proc/sys/kernel/kptr_restrict → 0`.

**Recommendation.** `kernel.kptr_restrict = 2` (hide from all users including root) in sysctl.

#### F-05 · Egress allowlist is host-tight but not semantically tight
- **Severity:** High · **Status:** Open (deferred) · **First seen:** 2026-05-22 · **Prior refs:** HIGH-06, backlog C2 (logged Critical)

**Description.** The allowlist blocks arbitrary DNS and direct-IP egress, but after resolving
`api.anthropic.com` via the gateway DNS, **TCP port 80 is reachable** alongside 443 — port 80 serves
no purpose for an HTTPS API. More broadly, the lock still permits whatever the allowed endpoint
exposes; the agent talks directly to the full provider API rather than through a narrow contract.

**Evidence.** Post-DNS TCP connect to the allowed host succeeds on both 80 and 443; direct-by-IP TCP
receives RST and non-DHCP UDP is dropped (good).

**Recommendation.** Restrict the TCP allowlist to **443 only**; block link-local/RFC1918 metadata
ranges. After F-02, put model traffic behind a narrow host-side proxy contract rather than direct
provider access (backlog C2).

#### F-06 · No cgroup resource limits
- **Severity:** High · **Status:** Open · **First seen:** 2026-05-22 · **Prior refs:** HIGH-01, backlog H5

**Description.** No memory/CPU/PID limits are applied to agent children. A runaway workload (fork
bomb, runaway build, memory exhaustion) can consume all VM resources indefinitely.

**Recommendation.** Set `memory.max`, `cpu.max`, and `pids.max` in the agent's cgroup v2 slice
(e.g. 2 GB memory, 150% CPU).

#### F-07 · No audit logging / no off-guest evidence forwarding
- **Severity:** High · **Status:** Open · **First seen:** 2026-05-22 · **Prior refs:** HIGH-02, backlog H7

**Description.** `auditd` is not running: no syscall-level trail, process-exec logging, or file-access
recording inside the guest. Broker audit exists host-side, but a compromised guest can tamper with
any local-only log.

**Recommendation.** Enable auditd with rules covering `execve`, `open`/`openat` on sensitive paths,
`ptrace`, network connect, and module load — and **forward off-guest via vsock** before the VM can
tamper with the records.

#### F-08 · Raw disk and physical memory devices present
- **Severity:** High · **Status:** Open · **First seen:** 2026-05-22 · **Prior refs:** HIGH-04, backlog H4

**Description.** `/dev/sda` (raw block device) and `/dev/mem` (physical memory) are present and
readable. With `CAP_SYS_RAWIO` now dropped for the agent (R-01) this is lower severity than at
original assessment, but the devices should be absent from the sandbox.

**Recommendation.** Bind-mount `/dev/null` over `/dev/mem`; remove `/dev/sda` from the VM `/dev` if
direct disk access is not required; enable kernel lockdown where compatible and mount with `nodev`.

#### F-09 · Cross-session filesystem mount exposure
- **Severity:** High · **Status:** Open · **First seen:** 2026-05-22 · **Prior refs:** HIGH-05, backlog H6

**Description.** The product uses one shared VM with per-session shares under `/sessions/<id>`.
Session separation therefore depends on the guest sandbox and mount permissions rather than a
hypervisor boundary; foreign session mounts may be observable inside the VM.

**Recommendation.** If tenant-style isolation becomes a goal, move to one session per VM, per-session
mount views, or read-only/hidden foreign mounts.

#### F-10 · Broker policy gate still `AllowAll` — irreversible tail ungated
- **Severity:** High · **Status:** Open · **First seen:** 2026-05-22 · **Prior refs:** backlog H2

**Description.** The broker has a `Gate` seam and audit log, but the active implementation is
`AllowAll`, so host-side enforcement is not yet real per-method/per-door policy. The policy engine in
the guest (`policy.ts`) correctly denies `WebFetch`/`WebSearch` and defaults-deny unknown tools, but
the host gate is the durable boundary.

**Recommendation.** Replace `AllowAll` with real policy. Keep routine work low-friction, but route
irreversible actions (destructive deletes, publishing, pushing, sending, broad egress changes)
through an `Ask` or `Deny` decision.

#### F-11 · Workspace writes are not reversible
- **Severity:** High · **Status:** Open · **First seen:** 2026-05-22 · **Prior refs:** backlog H1

**Description.** Egress lockdown protects confidentiality but does not protect the user's files from
destructive edits inside the allowed workspace.

**Recommendation.** Add snapshots, copy-on-write work areas, or explicit checkpoints before risky
operations.

#### F-12 · No runtime backstops for anomalous behavior
- **Severity:** High · **Status:** Open · **First seen:** 2026-05-22 · **Prior refs:** backlog H3

**Description.** There is no mechanism to halt or escalate after repeated denied tool calls,
unexpected egress attempts, or high-volume file/network activity.

**Recommendation.** Add backstops enforced **outside** the guest (so a compromised process cannot
disable them) that halt or escalate on those signals.

### Medium

#### F-14 · Speculation store bypass not mitigated
- **Severity:** Medium · **Status:** Open · **First seen:** 2026-05-24 · **Prior refs:** 2026-05-24 #6

**Description.** The VM is not applying SSBD; lower risk on Apple Silicon than x86 but relevant for
multi-tenant scenarios.

**Evidence.** `Speculation_Store_Bypass: vulnerable`, `SpeculationIndirectBranch: unknown`.

**Recommendation.** Add `spec_store_bypass_disable=seccomp` (or `=on`) to the kernel cmdline (current:
`console=hvc0 root=/dev/vda ro noresume init=/sbin/init`).

#### F-15 · Sensitive data readable in the agent home directory
- **Severity:** Medium · **Status:** Open · **First seen:** 2026-05-24 · **Prior refs:** 2026-05-24 #7

**Description.** `~/.claude.json` (contains a `userID` hash) and `~/.claude/policy-limits.json`
(reveals the restriction config) are readable. The files are owned by `atelier`, so the agent needs
read access.

**Recommendation.** Move policy-limits out of the agent-readable path, or encrypt the sensitive
fields.

#### F-16 · Kernel module loading enabled at runtime
- **Severity:** Medium · **Status:** Open · **First seen:** 2026-05-22 · **Prior refs:** MED-02

**Description.** New kernel modules can be loaded at runtime (`kernel.modules_disabled = 0`). With
`CAP_SYS_MODULE` dropped for the agent (R-01) the risk is reduced but not eliminated for
control-plane processes.

**Recommendation.** Set `kernel.modules_disabled = 1` post-boot (one-way) once no later dynamic module
load is needed; enable kernel lockdown where compatible.

#### F-17 · ICMP spoofed locally for all destinations
- **Severity:** Medium · **Status:** Open · **First seen:** 2026-05-22 · **Prior refs:** MED-03

**Description.** `gvforwarder` generates fake ICMP echo replies for all destinations without
forwarding packets, so ping always succeeds — even for unreachable hosts — masking network partitions
and misleading operators.

**Recommendation.** Drop ICMP outright, or forward it honestly.

#### F-18 · Tool-readable content is untrusted (prompt injection)
- **Severity:** Medium · **Status:** Open · **First seen:** 2026-05-22 · **Prior refs:** backlog (medium)

**Description.** Files, web pages, repo text, and tool errors can all carry prompt injection. Rendering
attacker-controlled markdown images or remote links in a privileged UI context compounds the risk.

**Recommendation.** Treat all tool-readable content as untrusted input; avoid rendering
attacker-controlled markdown images/remote links in privileged UI contexts.

---

## Resolved Findings

#### R-01 · Agent ran as unconstrained root with all capabilities — **Fixed & verified**
- **Severity:** Critical · **First seen:** 2026-05-22 · **Prior refs:** CRIT-01, backlog C3

The agent and children ran as `uid=0` with all 41 capabilities (incl. `CAP_SYS_MODULE`,
`CAP_SYS_ADMIN`, `CAP_SYS_PTRACE`, `CAP_SYS_RAWIO`, `CAP_DAC_OVERRIDE`). **Fix:** the agent is now
launched inside bubblewrap as uid/gid **1001** with `--cap-drop ALL` and fresh user/pid/ipc/uts/mnt
namespaces (net deliberately shared so egress still works), with the read-only root bind-mounted. A
subtle gap was caught and closed: guestd (PID 1, root) launching bwrap mapped sandbox-uid 1001 onto
host-uid-0 (`uid_map: 1001 0 1`), making the agent DAC-root; guestd now drops the child's real uid/gid
to 1001 (`SysProcAttr.Credential`) **before** exec'ing bwrap, so the namespace can only map to
host-1001. *Verified in-guest:* `id → uid=1001`; `CapEff/Prm/Bnd = 0`; `cat /etc/shadow → Permission
denied`; `/workspace`, `/home/atelier`, `/tmp` writable, `/run`, `/sessions` denied; full agent run
end-to-end. **Keep as an invariant for every non-operator execution path.**

#### R-02 · World-writable / mutable system filesystem — **Fixed & verified**
- **Severity:** Critical · **First seen:** 2026-05-22 · **Prior refs:** CRIT-05, backlog C4

System dirs and binaries were world-writable; root cause was `mke2fs -d` on a Windows host, which
cannot preserve Unix owner/mode. **Fix:** the ext4 is now populated inside a Linux imager container
(only the opaque blob crosses to the host); perms are normalized at build (`/usr`, `/etc`
`0755 root:root`; `/etc/passwd` `0644`; `/etc/shadow` `0640`); the root disk is mounted read-only
(`RootFSReadOnly` → SCSI `ReadOnly` + `ro` cmdline); writable paths are tmpfs with explicit modes
(`/run`, `/sessions` `0755`; `/home/atelier` `0700`; `/var/tmp` `1777`; `/tmp` per-sandbox `0755`).
*Verified:* `/usr/bin/bash 0755 root:root`, `/etc/shadow 0640`, `/` is `ro`, write to `/usr/bin →
EROFS`.

#### R-03 · Zero namespace isolation between processes — **Substantially addressed**
- **Severity:** Critical · **First seen:** 2026-05-22 · **Prior refs:** CRIT-03

Every process originally shared all namespaces with PID 1. **Fix:** bubblewrap gives the agent its own
user, pid, ipc, uts, and mnt namespaces; the network namespace is intentionally shared (the egress
jail is the boundary). The trusted control-plane processes (guestd, gvforwarder) still share
namespaces among themselves.

#### R-04 · SSH host keys world-readable — **Resolved**
- **Severity:** High · **First seen:** 2026-05-22 · **Prior refs:** HIGH-03

`openssh-server` (an unused listener) was removed from the image, so there are no host keys to expose.

#### R-05 · DHCP client removed; static network config — **Applied** (hardening context)
- **First seen:** 2026-05-22

`isc-dhcp-client` was dropped. The guest now configures `tap0` statically (192.168.127.2, gw .1, fixed
MAC) and runs gvforwarder with `-preexisting`. The host-side DNS-sinkhole + TCP allowlist is unchanged;
egress allow/deny was re-verified (`api.anthropic.com` reachable; other hosts `NXDOMAIN`).

#### F-13 · No seccomp filter — **Fixed & verified**
- **Severity:** Medium · **First seen:** 2026-05-22 · **Prior refs:** 2026-05-24 #5, CRIT-02 (logged Critical), backlog C5

A cBPF profile is now installed for every non-privileged exec via `bwrap --seccomp <fd>`
(`services/cmd/guestd/sandbox_linux.go`). The profile is Docker's default seccomp allowlist, vendored
at `image/agent/seccomp/default.json` and compiled to an arch-correct blob by `compile-seccomp.py`
**inside the target-arch agent image** (`--platform linux/{amd64,arm64}`), then packed onto the
read-only guestd volume at `/opt/guestd/seccomp.bpf`. guestd opens the blob and hands it to the bwrap
child on fd 3 (Go `ExtraFiles[0]`); a missing/unreadable blob **fails the exec closed** rather than
running unfiltered. The privileged operator escape hatch is unchanged (no filter, by design). The
profile is evaluated as a **no-capability** process (matching `--cap-drop ALL`), so the
CAP_SYS_ADMIN-gated entries (`unshare`, `setns`, `mount`, `clone3`, `bpf`, `perf_event_open`, …) fall
through to the default `ERRNO(EPERM)` and `clone` is restricted to non-namespace flags. *Verified:*
the compiled blob blocks `unshare(CLONE_NEWUSER)` with `EPERM` while still allowing thread creation
(`clone3`→`ENOSYS`→`clone` fallback) and `execve`; guestd unit tests cover the `--seccomp 3` wiring
and the fail-closed path; the blob is confirmed packed at `/opt/guestd/seccomp.bpf` (mode 0644). A
live VZ boot (`npm run e2e:host`) confirms a sandboxed exec runs under `Seccomp: 2` / `NoNewPrivs: 1`,
`unshare(CLONE_NEWUSER)` returns `EPERM`, and both agent loops (one-shot + serve) complete
unstrangled.

#### F-01 · Unprivileged user-namespace creation grants full capabilities in-namespace — **Fixed & verified**
- **Severity:** Critical · **First seen:** 2026-05-24 · **Prior refs:** 2026-05-24 #1

Closed by F-13. The seccomp profile denies the namespace-creation syscalls for the no-capability
agent: `unshare`/`setns` return `EPERM`, `clone3` returns `ENOSYS`, and `clone` is permitted only when
no `CLONE_NEW*` flag is set (`arg0 & 0x7E020000 == 0`), so a fresh user namespace can no longer be
created. *Verified:* `unshare --user --map-root-user id` now fails with `EPERM` (was `uid=0` with
`CapEff 000001ffffffffff`); thread creation and normal subprocess exec are unaffected.

---

## Host-Side Issues (broker)

Issues found during verification that live on the host, not in the guest sandbox.

| ID | Issue | Status |
|---|---|---|
| HS-01 | `stopVM` reported a phantom HCS error (`HcsCloseComputeSystem hresult=0x8f3a51f0`); the binding read a return value from a documented `void` function | **Fixed** (binding no longer reads the return) |
| HS-02 | Broker can panic on VM teardown (`linuxkit/virtsock` hvsock `ioCompletionProcessor` — "The handle is invalid") when a VM is stopped mid-handshake | **Open** — pre-existing third-party-library race |

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
| Seccomp cBPF filter applied (Docker default profile, no-cap) | ✅ |
| Unprivileged user-namespace creation blocked (`unshare`/`clone(CLONE_NEW*)` denied) | ✅ |
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

## Remediation Priority

Ordered by leverage; references the finding IDs above (no re-description here).

1. **F-02** — move the Anthropic credential to a host-side proxy → closes the highest-value data finding.
2. **F-03** — `--tmpfs /opt` in bwrap → hides the host-comms binary and runtime source.
3. **F-04** — `kernel.kptr_restrict = 2` → hides kernel pointers.
4. **F-05** — restrict egress to 443 + block metadata ranges → tightens the network lock.
5. **F-06** — cgroup v2 limits → contains runaway workloads.
6. **F-10** — replace the broker `AllowAll` gate → gates the irreversible tail.
7. **F-14, F-16** — `spec_store_bypass_disable` on cmdline; `modules_disabled = 1` post-boot.

> **Done:** F-13 + F-01 — seccomp profile blocking `CLONE_NEWUSER` (the former #1) is implemented and
> verified; see Resolved Findings.

---

## Architecture Notes

The vsock transport (`vmw_vsock_virtio_transport`) is the guestd ↔ host channel. The broker RPC
protocol (`protocol.json`) exposes powerful operations including `setEgressPolicy` and
`attachWorkspace`; keeping the vsock channel unreachable from inside the bwrap sandbox
(no `/dev/vsock` exposed — ✅ already the case) is important to maintain.

The policy engine (`policy.ts`) denies `WebFetch`/`WebSearch` and defaults to deny for unknown tools.
The `AllowAll` host-side gate (F-10) is the remaining gap.

All VM network traffic flows through a user-space proxy chain:

```
VM (tap0) ──► gvforwarder (user-space, /dev/net/tun)
           ──► vsock://2:1024 (HyperV VMBus)
           ──► Host-side proxy (enforces allowlist policy)
           ──► Internet
```

The host-side vsock proxy implements a **DNS-sinkhole + TCP allowlist** pattern:
- Only `api.anthropic.com` resolves via the gateway DNS (all others return `NXDOMAIN`).
- TCP connections are only permitted after a DNS lookup for an allowed hostname.
- Direct-by-IP TCP connections receive an instant RST.
- All UDP (except DHCP) is silently dropped.

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
