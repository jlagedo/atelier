# IPC Security — Hop 2 (App to Host Broker)

Scope: securing **Hop 2**, the IPC boundary between the unprivileged desktop app
and the privileged host broker. For the full component map and the other hops see
[`runtime-architecture.md`](runtime-architecture.md). For VM-internal hardening,
see [`vm-hardening.md`](vm-hardening.md).

## Why Hop 2 is the boundary that matters

| Side | Identity | Privilege |
|------|----------|-----------|
| Electron **main** (Hop 2 client) | runs as the interactive user | unprivileged |
| Host **broker** (`cmd/atelierd`, Hop 2 server) | elevated process now; planned Windows service | privileged |

The pipe exposes the **full privileged surface** — `exec`, `readFile`/`writeFile`,
`setEgressPolicy`, plus VM lifecycle (`pkg/protocol/protocol.go`). Anyone who can
open the pipe *and* pass the gate can drive the sandbox and the host-side file
jail. So Hop 2 is the privilege boundary.

**Threat:** another local process driving the broker's privileged methods by
opening the pipe / socket.

**Non-threat:** on-wire eavesdropping. The channel is a local, kernel-mediated
named pipe / unix socket — **TLS/mTLS buys nothing here.** The real controls are
*who can open it*, *who you prove they are*, and *what they're allowed to call*.

## Current State

- **Windows:** `winio.ListenPipe(addr, &winio.PipeConfig{MessageMode:false})` sets
  **no `SecurityDescriptor`** → permissive default DACL
  (`services/internal/rpc/transport_windows.go`).
- **Unix dev transport:** unix socket at **`/tmp/atelierd.sock`** with default umask
  perms — `/tmp` is world-accessible
  (`services/internal/rpc/transport_unix.go`).
- **Both:** the policy gate is `AllowAll` (`services/internal/broker/policy.go`) —
  every method is permitted, though each check is still audited.

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
  (`~/…/atelierd.sock`, mode `0600`, owned by the user) or a system helper
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

### L4 — per-method authorization gate

Replace `AllowAll` (`services/internal/broker/policy.go`) with a `Gate` that does
**allow/ask/deny per method+door** and audit-logs every call. Sensitive doors
(`exec`, `writeFile`, `setEgressPolicy`) should be policy-controlled even if the
connection boundary is bypassed. The renderer already has display-only policy cards;
an `Ask` flow can be added later if the product needs human approval.

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
