# VM Sandbox Security

| Field | Detail |
|---|---|
| Purpose | Track current risks in the guest VM containment boundary. |
| Primary reader | Engineers changing guest sandboxing, credentials, egress, or policy. |
| Scope | Autonomous agent working on local files inside the utility VM. |
| Out of scope | App-to-Broker IPC; see [`ipc-security.md`](ipc-security.md). |
| Raw evidence | [`audits/2026-05-24-vz-guest-assessment.md`](audits/2026-05-24-vz-guest-assessment.md). |
| External cross-check | [`sandbox-escape-benchmark-2026.md`](sandbox-escape-benchmark-2026.md) — cage vs. SANDBOXESCAPEBENCH ([arXiv:2603.02277](https://arxiv.org/abs/2603.02277)). |

This consolidated register supersedes the old top-level `security.md`. That file
marked F-03, F-04, F-06, F-09, and F-16 open; the newer audit and current code show
them resolved.

## Assessment Timeline

| Date | Environment | Notes |
|---|---|---|
| 2026-05-22 | Ubuntu 22.04.5 LTS · Linux 6.8 · x86_64 · Hyper-V | Initial HCS assessment and first remediation pass |
| 2026-05-24 | Ubuntu 24.04.4 LTS · Linux 6.17 · aarch64 · Apple Silicon VZ | Follow-up audit after the macOS/VZ port |
| 2026-05-25 | Current tree evidence | bwrap narrowing, cgroups, sysctls, module latch, and Landlock present in code |

## Current Posture

The isolation spine is strong: a dedicated Linux utility VM, read-only root disk,
default-deny host-mediated egress, bubblewrap, seccomp, uid/gid 1001, per-exec
cgroups, and a Landlock shim for non-privileged agent execs.

Remaining risk is concentrated in credential residency, Hop-2 policy/access
control, write rollback, and observability.

| Area | Current state | Code reference | Finding |
|---|---|---|---|
| Hypervisor boundary | Dedicated Linux VM under VZ or HCS | `services/internal/vmm`, `services/internal/hcs` | — |
| Agent identity | uid/gid 1001, all capabilities dropped | `services/cmd/runner/sandbox_linux.go` | R-01 |
| Filesystem sandbox | Curated bwrap allow-list, no whole-root bind | `services/cmd/runner/sandbox_linux.go` | F-03, F-09 resolved |
| Seccomp | cBPF profile applied via `bwrap --seccomp 3` | `image/agent/seccomp`, `services/cmd/runner/sandbox_linux.go` | F-01, F-13 resolved |
| Resource limits | Per-exec cgroup v2 caps | `services/cmd/runner/cgroup_linux.go` | F-06 resolved |
| Kernel hardening | `kptr_restrict=2`, `ptrace_scope=2`, `modules_disabled=1` | `image/guest/init.sh` | F-04, F-16 resolved |
| Landlock | Best-effort FS allow-list + outbound TCP 443 | `services/cmd/atelier-landlock/main.go` | F-05 mitigation |
| Egress | DNS allowlist + IP pinning in host netjail | `services/internal/netjail` | F-05 |
| Model credential | Still injected into the in-guest agent env | `apps/desktop/src/main/sessions/manager.ts`, `packages/partisan/cli_guest.py` | F-02 |
| Broker gate | `AllowAll` | `services/internal/broker/policy.go` | F-10 |

**Tracked open findings:** 1 critical, 5 high, 4 medium.

## Open Findings

### Critical

#### F-02 · Model API key resident in guest environment and memory

- **Severity:** Critical
- **Status:** Open
- **First seen:** 2026-05-22

`ANTHROPIC_API_KEY` is passed into the in-guest agent process environment. The
live path is now partisan, but the exposure class is the same: any process running
as the agent user can inspect its own environment or inherited child environments.

**Recommendation.** Terminate model calls at a host-side authenticated proxy. Have
the guest send requests with no ambient provider key. Inject a scoped, revocable
credential on the host. Interim: prefer a file/FD scoped to the agent over an env
var.

### High

#### F-05 · Egress allowlist is host-tight but not semantically tight

- **Severity:** High
- **Status:** Open, partially mitigated
- **First seen:** 2026-05-22

The host allowlist blocks arbitrary DNS and direct-IP egress, but
`netjail.AllowIP` currently permits any TCP port for a recently pinned IP. The
Landlock shim narrows non-privileged agent connects to TCP 443, but the durable
host boundary also needs port and protocol semantics.

**Recommendation.** Make `netjail` enforce TCP 443 for model/package hosts unless
a narrower per-host policy explicitly allows more. Continue to block link-local and
RFC1918 metadata ranges.

#### F-07 · No syscall audit logging / no off-guest evidence forwarding

- **Severity:** High
- **Status:** Open
- **First seen:** 2026-05-22

Broker audit exists host-side, but there is no guest syscall/process/file evidence
stream that survives guest compromise.

**Recommendation.** Add off-guest forwarding for high-signal records: exec,
open/openat on sensitive paths, ptrace, network connect, and module-load attempts.

#### F-10 · Broker policy gate is `AllowAll`

- **Severity:** High
- **Status:** Open
- **First seen:** 2026-05-22

The broker has a `Gate` seam and audit log, but the active gate still allows every
method. The in-guest agent policy constrains normal tool use, but the broker is the
privileged boundary for Hop 2 callers.

**Recommendation.** Replace `AllowAll` with per-method allow/ask/deny policy.
Gate `exec`, `writeFile`, `setEgressPolicy`, lifecycle operations, and any future
irreversible capability at the broker.

#### F-11 · Workspace writes are not reversible

- **Severity:** High
- **Status:** Open
- **First seen:** 2026-05-22

The cage limits where writes land, but it does not provide rollback for destructive
edits inside the allowed workspace.

**Recommendation.** Add snapshots, copy-on-write work areas, or explicit
checkpoints before risky operations.

#### F-12 · No runtime backstops for anomalous behavior

- **Severity:** High
- **Status:** Open
- **First seen:** 2026-05-22

There is no host-enforced halt/escalation after repeated denied tool calls,
unexpected egress attempts, or unusual file/network volume.

**Recommendation.** Add host-side backstops that stop or escalate on those signals.

### Medium

#### F-14 · Speculation Store Bypass not mitigated

- **Severity:** Medium
- **Status:** Open
- **First seen:** 2026-05-24

The VZ audit observed `Speculation_Store_Bypass: vulnerable`. Lower risk on a
single-user desktop VM, but relevant if the model moves toward tenant isolation.

**Recommendation.** Add `spec_store_bypass_disable=seccomp` or `=on` to the kernel
cmdline if compatible.

#### F-15 · Sensitive agent-home config readable by the agent

- **Severity:** Medium
- **Status:** Needs re-verification after the partisan cutover
- **First seen:** 2026-05-24

The raw audit found `~/.claude.json` and `~/.claude/policy-limits.json` readable in
the old artisan/Claude-agent path. The live agent is now partisan/OpenHands, so the
exact files need re-checking. The class remains: keep control-plane policy and
user identity material out of agent-readable paths unless the agent truly needs it.

**Recommendation.** Re-audit the partisan home directory and move policy metadata
out of agent-readable paths where possible.

#### F-17 · ICMP spoofed locally for all destinations

- **Severity:** Medium
- **Status:** Open
- **First seen:** 2026-05-22

The gvisor-tap-vsock stack may synthesize ICMP echo success, which can mislead
operators about actual reachability.

**Recommendation.** Drop ICMP outright or forward it honestly.

#### F-18 · Tool-readable content is untrusted

- **Severity:** Medium
- **Status:** Open
- **First seen:** 2026-05-22

Files, tool errors, package output, and web content can all carry prompt injection.
This combines with F-02 while a live provider key exists in the guest.

**Recommendation.** Treat tool-readable content as untrusted input and avoid
rendering attacker-controlled remote links/images in privileged UI contexts.

## Resolved Findings

| ID | Status |
|---|---|
| R-01 | Agent no longer runs as unconstrained root; sandboxed execs run as uid/gid 1001 with all capabilities dropped. |
| R-02 | Root filesystem is read-only; writable runtime paths are tmpfs/session mounts with explicit modes. |
| R-03 | Agent gets user/pid/ipc/uts/mount namespace isolation through bwrap. |
| R-04 | Unused OpenSSH server/host-key exposure removed. |
| R-05 | DHCP client removed from the guest path; static tap/gvforwarder network path is used. |
| F-01 | Fresh unprivileged user namespace creation blocked by seccomp. |
| F-03 | Runner volume no longer exposed by a whole-root bind; the sandbox gets a curated allow-list. |
| F-04 | `kernel.kptr_restrict = 2` set at boot. |
| F-06 | Per-exec cgroup v2 limits applied: pids, memory, swap, CPU. |
| F-08 | Raw disk/vsock-style device exposure addressed for non-privileged sandboxed execs by the fresh bwrap `/dev` and narrowed bind set. |
| F-09 | Sibling session mounts hidden from each sandbox; only the exec's own workspace is bound. |
| F-13 | Seccomp cBPF profile applied fail-closed for sandboxed execs. |
| F-16 | Kernel module loading latched off after required modules load. |

## Remediation Priority

1. **F-02** — host-side model credential proxy.
2. **F-10** — replace broker `AllowAll` with real policy.
3. **F-05** — host-enforced port/protocol policy in `netjail`.
4. **Hop 2 L1/L2** — restrict pipe/socket access and verify the caller; see
   [`ipc-security.md`](ipc-security.md).
5. **F-11/F-12** — workspace rollback and anomaly backstops.
6. **F-07** — off-guest evidence forwarding.
7. **F-14/F-15/F-17/F-18** — targeted hardening and re-verification.

## Source References

- Guest sandbox: `services/cmd/runner/sandbox_linux.go`
- Landlock shim: `services/cmd/atelier-landlock/main.go`
- Cgroups: `services/cmd/runner/cgroup_linux.go`
- Boot hardening: `image/guest/init.sh`
- Seccomp profile: `image/agent/seccomp`
- Egress policy: `services/internal/netjail/filter.go`
- Broker gate: `services/internal/broker/policy.go`
- Live agent key resolver: `packages/partisan/cli_guest.py`

*Classification: Internal / Security Sensitive*
