# Security Hardening — Hop 2 (App ⇄ Host Broker)

Scope: securing **Hop 2**, the IPC boundary between the unprivileged desktop app
and the privileged host broker. For the full component map and the other hops see
`docs/architecture.md`.

## Why Hop 2 is the boundary that matters

| Side | Identity | Privilege |
|------|----------|-----------|
| Electron **main** (Hop 2 client) | runs as the interactive user | unprivileged |
| Host **broker** (`cmd/host`, Hop 2 server) | ships as LocalSystem (Win) / root helper (mac) | privileged |

The pipe exposes the **full privileged surface** — `exec`, `readFile`/`writeFile`,
`setEgressPolicy`, plus VM lifecycle (`pkg/protocol/protocol.go`). Anyone who can
open the pipe *and* pass the gate can drive the sandbox and the host-side file
jail. So Hop 2 is the privilege boundary.

**Threat:** another local process driving the broker's privileged methods by
opening the pipe / socket.

**Non-threat:** on-wire eavesdropping. The channel is a local, kernel-mediated
named pipe / unix socket — **TLS/mTLS buys nothing here.** The real controls are
*who can open it*, *who you prove they are*, and *what they're allowed to call*.

## Current state (the gap)

- **Windows:** `winio.ListenPipe(addr, &winio.PipeConfig{MessageMode:false})` sets
  **no `SecurityDescriptor`** → permissive default DACL
  (`services/internal/rpc/transport_windows.go`).
- **macOS/unix:** unix socket at **`/tmp/atelier-host.sock`** with default umask
  perms — `/tmp` is world-accessible
  (`services/internal/rpc/transport_unix.go`).
- **Both:** the policy gate is `AllowAll` (`services/internal/broker/policy.go`) —
  every method is permitted.

Net: Hop 2 has no meaningful access control today. This is **L0** below.

## The hardening ladder

The same five levels apply on both platforms; the *mechanisms* differ. L1–L3 gate
**who connects**; L4 gates **what they can do** and is the real containment.

| Level | Control | Threat closed | Effort |
|-------|---------|---------------|--------|
| L0 | default ACL + `AllowAll` | — | — |
| L1 | restrict the pipe/socket to a principal | random / other-user processes | low |
| L2 | tight ACL + verify the caller is our signed binary | squatting/MITM; unsigned callers | medium |
| L3 | app-layer auth token | unsigned caller running as the user | medium (optional) |
| L4 | per-method allow/ask/deny gate + approvals + audit | privileged-capability abuse by any caller | high |

### L1 — restrict who can open the endpoint

- **Windows:** set `PipeConfig.SecurityDescriptor` to a **protected** SDDL granting
  only LocalSystem, Administrators, and a dedicated install-time group
  (`atelier-users`, the `docker-users` analogue):

  ```
  D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;<atelier-users-SID>)
  ```

- **macOS:** move the socket **off `/tmp`**. Either per-user
  (`~/…/atelier-host.sock`, mode `0600`, owned by the user) or a system helper
  socket in `/var/run/atelier/` owned by root, mode `0660` + a dedicated group.

**Stops:** arbitrary unprivileged / other-user processes. **Doesn't stop:** a
process running as a member user. **This is the minimum to ship.**

### L2 — verify the caller is our binary

Independent of the ACL, confirm the connecting process is really our app.

- **Windows:** `GetNamedPipeClientProcessId` (kernel32 via `x/sys/windows`) →
  resolve the client image → verify **Authenticode signature *and* publisher
  identity** (check the leaf cert subject/thumbprint is ours — "is signed" alone is
  worthless). Also: `FILE_FLAG_FIRST_PIPE_INSTANCE` to block name squatting; on the
  client, dial with `SECURITY_SQOS_PRESENT | SECURITY_IDENTIFICATION` and verify the
  server pipe's **owner SID is LocalSystem** (anti-MITM).
- **macOS (stronger + first-class):** from the connection get the peer's **audit
  token** (`LOCAL_PEERTOKEN`, or `xpc_connection_get_audit_token` on XPC), build a
  `SecCode` via `SecCodeCopyGuestWithAttributes`, and check it against a pinned
  requirement with `SecCodeCheckValidity` / `SecRequirementCreateWithString`:

  ```
  anchor apple generic and identifier "com.atelier.app"
    and certificate leaf[subject.OU] = "<YOUR_TEAM_ID>"
  ```

  Use the **audit token, not the PID**, to avoid PID-reuse TOCTOU. On XPC,
  `xpc_connection_set_peer_code_signing_requirement()` (macOS 12+) enforces this
  declaratively.

**Stops:** squatting/MITM and unsigned callers; on macOS, even a process running as
the same user (it can't forge the signature). **Doesn't stop:** code injection into
the signed process or a fully compromised account — that's L4's job.

### L3 — app-layer auth token (optional)

Mint a per-install/launch secret shared by the legit app and service (DPAPI file or
ACL'd registry key on Windows; redundant on macOS given L2). Require it in a
handshake. Modest marginal gain over L2's signature check — **only worth it if you
can't rely on signature verification.**

### L4 — per-method authorization gate (the real containment)

Replace `AllowAll` (`services/internal/broker/policy.go`) with a `Gate` that does
**allow/ask/deny per method+door**, routes sensitive doors (`exec`, `writeFile`,
`setEgressPolicy`) through a **human-in-the-loop approval UI**, and audit-logs every
call (audit scaffolding exists). Platform-independent — same Go code on Windows and
macOS. Assumes the transport boundary *can* be breached and still limits the damage.
**Highest-value control.**

## Code-signing cost (the L2 cert)

Two different reasons to sign, very different cost answers:

1. **For the L2 broker→app check only** — you do **not** need a publicly-trusted
   cert. The broker checks the leaf cert subject/thumbprint against a value *you*
   hardcode, so a **self-signed cert + thumbprint pin works and costs $0.**
2. **For end-user distribution** (no SmartScreen / Gatekeeper warnings) — you need
   a publicly-trusted cert:
   - **Windows:** OV ~$215–$225/yr (slow reputation) or **EV ~$279/yr** (instant
     SmartScreen reputation; usually needs a hardware token / HSM). Cheapest EV ≈
     $279 from resellers (CheapSSLsecurity/SignMyCode, Sectigo brand). Note the 2026
     CA/B rule capping validity at ~15 months.
   - **macOS:** Apple **Developer ID** via the Apple Developer Program, **$99/yr**,
     required for signing + notarization anyway — so the L2 check is effectively
     free on Mac.

## Recommendation

1. **L1 everywhere** — must-ship floor (SDDL on Windows; off-`/tmp` + mode on mac).
2. **L2** — cheap and high-value; on macOS it's free (Developer ID) and stronger.
3. **Skip L3** unless signature verification isn't an option.
4. **Invest in L4** — L1–L3 only gate *who connects*; L4 gates *what they can do*,
   which is where the irreversible capabilities live.
5. **No TLS/mTLS** — wrong tool for a local IPC channel.

## Source references

- Windows transport (no SD today): `services/internal/rpc/transport_windows.go`
- Unix transport (`/tmp` socket today): `services/internal/rpc/transport_unix.go`
- Policy gate (`AllowAll` today): `services/internal/broker/policy.go`
- Audit logging: `services/internal/broker/audit.go`
- Method surface: `services/pkg/protocol/protocol.go`
- Broker authorize chokepoint: `services/internal/broker/broker.go` (`authorize`)

---

# Securing the VM for Unattended AI Execution

Scope: securing the **guest VM as the containment boundary** for an autonomous
(no human-in-the-loop per action) Claude agent that works on local files. This
complements the Hop-2 section above (which secures the App⇄Broker IPC boundary)
and the internal audit in `docs/vm_security_report.md` (which enumerates VM
internals). Tips below are prioritized by severity and compared against peer
products and authoritative guidance.

## Where Atelier already stands

The isolation spine is at or above best-in-class. Prioritize the real gaps, not
the strengths.

| Dimension | Atelier today | Peer comparison |
|---|---|---|
| Isolation primitive | **Hyper-V VM** (separate kernel) | Stronger than Anthropic Claude Code & OpenAI Codex (OS-level bwrap/Seatbelt, *no* VM). Same tier as Devin, Jules, E2B (Firecracker), Modal (gVisor). |
| Network egress | **Default-deny → single host** (`api.anthropic.com:443`) | Tighter than most. Codex defaults to *no* network; Anthropic/Modal use allowlists. Single-host is the strict end. |
| Privilege boundary (host) | Broker policy gate (L4 design), renderer hardened | Matches the OWASP "gate the irreversible tail" pattern. |

The throughline from every authoritative source (Willison's "lethal trifecta,"
Anthropic, OWASP LLM Top 10, NIST AI RMF): **only architecture that makes
harmful actions structurally impossible is dependable — guardrails/detection
never guarantee.** The VM + default-deny egress is the right spine. The work is
hardening the *residual* channels and the *inside* of the cage.

Two findings not covered by `vm_security_report.md`, which reframe the priorities:

1. **The API key inside the guest is the #1 divergence from every peer.**
   Anthropic (web), Codex cloud, and Jules keep the model credential *outside*
   the agent's environment via a host-side proxy. Atelier injects the key into
   the guest, where the audit found it in env vars, `/proc`, and 73 memory
   regions (CRIT-04).
2. **A default-deny allowlist to `api.anthropic.com` is not airtight.** Pluto
   Security's reverse-engineering of Claude Cowork showed data exfiltrated
   *through* the allowed Anthropic endpoint, by having the agent use an
   **attacker-supplied API key** to upload files to the Files API and retrieve
   them later: "egress allowlisting without semantic understanding of the
   allowlisted endpoints provides a false sense of containment."

These combine: an agent that both *holds a real key* and *can reach the API*
partially defeats the egress lock. Hence they top the list.

## 🔴 Critical — fixes the architecture, not just the config

**C1 — Move the Anthropic credential out of the guest entirely.** Terminate the
model call at a **host-side authenticated proxy** (reuse the egress chain:
`gvforwarder → host gvisor-tap-vsock`). The guest sends model requests with *no*
key; the host injects a scoped credential. Closes CRIT-04 (key in
env/`/proc`/memory) *and* blunts the Cowork-style exfil-via-allowed-domain attack
(the agent can no longer authenticate as anyone). This is the Anthropic-web /
Codex-cloud model. *Highest-value architectural change.*

**C2 — Make the egress lock semantically tight, not just host-tight.**
- Block at the **syscall layer** (seccomp: deny arbitrary `socket()`/DNS), not
  only by IP/domain — Cowork blocks `socket()` outright.
- **Close port 80** (HIGH-06) — useless for an HTTPS API, only a downgrade/exfil path.
- If C1 isn't done first: scope/pin the credential so the agent can't auth as a
  different account; block writes to Files/upload endpoints if unneeded.
- Block cloud-metadata `169.254.169.254` and RFC-1918 ranges (SSRF/lateral).

**C3 — Drop root, drop capabilities, no-new-privs** (CRIT-01). Run the agent as a
dedicated non-root UID, `cap-drop=ALL` (add back nothing — a coding agent needs
none), set `PR_SET_NO_NEW_PRIVS`. Collapses the largest privilege surface.
OpenHands ships `cap-drop ALL` + `no-new-privileges` by default.

**C4 — Immutable / read-only rootfs + fix world-writable system files**
(CRIT-05). Mount rootfs `ro` (or overlayfs read-only lower layer); writable
scratch only via tmpfs `noexec,nosuid,nodev`. Fix `/usr/bin`, `/etc`,
`/etc/passwd` (644 root:root), `/etc/shadow` (640). Consider dm-verity on the
lower layer. Bake at **image build time** (`image/build.sh`), not runtime.

**C5 — Seccomp default-deny + `ptrace_scope`** (CRIT-02). Docker default seccomp
profile as the floor, then explicitly block `ptrace`, `process_vm_readv/writev`,
`kexec_load`, `init_module`/`finit_module`, `bpf`, `mount`. Set
`kernel.yama.ptrace_scope=2` (or 3). Closes cross-process memory scraping — the
layered fix for any secret that does remain in memory.

## 🟠 High — containment the VM boundary alone doesn't give

**H1 — Make the VM/workspace ephemeral and snapshotted (integrity +
reversibility).** Egress lockdown protects *confidentiality* but does **nothing
against `rm -rf` on the user's files** — the gap the audit misses entirely. Peers
lean on disposability (E2B/Daytona fresh-per-task; Devin/Jules per-task VM;
Claude-web "automatic cleanup"). Snapshot the workspace before a run, operate on
a copy where feasible, treat the guest as disposable per task. NIST warns agents
"can initiate irreversible real-world actions before any human can intervene."

**H2 — Human gate on the *irreversible tail only* — even when unattended.** Don't
gate every action (Anthropic data: users rubber-stamp 93% of prompts). Gate the
small irreversible set: `git push`, publish, send, delete outside workspace
scope. Maps onto the **L4 broker policy gate** (`policy.go`, currently
`AllowAll`) — route `exec`/`writeFile`/`setEgressPolicy` through allow/ask/deny.
OWASP LLM06 (Excessive Agency) names exactly these scenarios.

**H3 — Runtime backstops for the missing human.** Adopt Anthropic Auto Mode's
pattern: halt-and-escalate after N consecutive/total tool denials (3/20), plus
egress-volume anomaly detection.

**H4 — Kill raw hardware access** (HIGH-04). Enable kernel **lockdown
(confidentiality mode)** / `CONFIG_STRICT_DEVMEM` to disable `/dev/mem`,`/dev/kmem`;
remove `/dev/sda` from the device list; `nodev` on mounts. Adopt a **minimal
device model** (Firecracker exposes only virtio-net/-block + a one-button
keyboard — "every feature not implemented is attack surface that doesn't exist").

**H5 — cgroup v2 resource limits** (HIGH-01). `memory.max`, `cpu.max`,
`pids.max` (fork-bomb defense). DoS containment.

**H6 — One session per VM, or read-only foreign mounts** (HIGH-05/MED-01). The
cross-session mount breaks tenant isolation — the audit found two other users'
files writable. Mount only the session's own workspace.

**H7 — SSH host-key perms** (`chmod 600`, HIGH-03) and **auditd** (execve /
ptrace / module-load rules, immutable `-e 2`, **forwarded off-VM** so a rooted
guest can't wipe the trail; HIGH-02).

## 🟡 Medium — hardening polish

- **M1** — `kernel.kptr_restrict=2`, `kernel.modules_disabled=1` (post-boot),
  `dmesg_restrict=1`, `unprivileged_bpf_disabled` (MED-01/02). Persist in
  `/etc/sysctl.d/`.
- **M2** — Stop faking ICMP replies in `gvforwarder` (MED-03) — drop or forward
  honestly; the spoof masks filter policy from monitoring.
- **M3** — Treat **all** tool-reachable content as untrusted (web pages, files,
  repo contents, *even tool error messages*); fetched content in an isolated
  context window. Don't render attacker-controlled markdown images/links (a known
  exfil channel).

## Defense-in-depth: the tie-together

Every AI-sandbox source (Firecracker, Northflank, innoq, Cloudflare) **assumes
the guest will be probed and insists on hardening it as if the hypervisor could
fail.** The Hyper-V boundary is necessary but not sufficient. The audit findings
(root, no seccomp, world-writable fs) are real defense-in-depth gaps — but the
two highest-leverage moves aren't in that audit: **(C1) get the credential out of
the guest** and **(H1/H2) make destructive actions reversible + gate the
irreversible tail.** Those address what the VM boundary structurally cannot.

## Source references

- Anthropic, "Making Claude Code more secure and autonomous" (sandboxing, 2025-10-20):
  https://www.anthropic.com/engineering/claude-code-sandboxing
- Anthropic, "Claude Code auto mode" (2026-03-25):
  https://www.anthropic.com/engineering/claude-code-auto-mode
- Anthropic, `anthropic-experimental/sandbox-runtime`:
  https://github.com/anthropic-experimental/sandbox-runtime
- OpenAI Codex, agent approvals & security:
  https://developers.openai.com/codex/agent-approvals-security
- Pluto Security, "Inside Claude Cowork" (allowlist exfil bypass):
  https://pluto.security/blog/inside-claude-cowork-how-anthropics-autonomous-agent-actually-works/
- Simon Willison, "The lethal trifecta for AI agents" (2025-06-16):
  https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/
- OWASP Top 10 for LLM Applications 2025 (LLM01/LLM02/LLM06):
  https://genai.owasp.org/llm-top-10/
- NIST AI RMF Generative AI Profile (NIST-AI-600-1):
  https://www.nist.gov/publications/artificial-intelligence-risk-management-framework-generative-artificial-intelligence
- Firecracker production host setup (jailer, seccomp, minimal device model):
  https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md
- Northflank, "How to sandbox AI agents" (defense-in-depth):
  https://northflank.com/blog/how-to-sandbox-ai-agents
- innoq, "Sandboxing coding agents' network" (default-deny egress):
  https://www.innoq.com/en/blog/2026/03/dev-sandbox-network/
- CIS Benchmarks (mount hardening, sensitive-file perms):
  https://www.cisecurity.org/cis-benchmarks
