# VM Hardening Roadmap

Scope: securing the **guest VM as the containment boundary** for an autonomous
Claude agent that works on local files. This complements
[`ipc-security.md`](ipc-security.md), which covers the App-to-Broker IPC boundary,
and [`vm-security-assessment.md`](vm-security-assessment.md), which preserves the
live audit findings and remediation status.

This document is the forward-looking hardening backlog. It should track what is
implemented, what remains open, and why the order matters.

## Current Posture

The isolation spine is strong: Atelier uses a Hyper-V utility VM, a default-deny
host-mediated egress path, a read-only root disk, and bubblewrap for the agent
process. The remaining risk is concentrated in key residency, syscall filtering,
resource limits, and shared-VM session separation.

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

## Critical

**C1 — Move the Anthropic credential out of the guest entirely.**  
Terminate model calls at a host-side authenticated proxy. The guest should send
model requests with no ambient API key; the host injects a scoped credential.
This closes the highest-value open finding from the audit: CRIT-04, where the key
is visible in the guest process environment and memory. It also reduces the
impact of exfiltration attempts through the allowlisted Anthropic endpoint.

**C2 — Make the egress lock semantically tight, not just host-tight.**  
The current allowlist blocks arbitrary DNS and direct-IP egress, but it still
allows whatever authenticated behavior the permitted endpoint exposes. After C1,
put model traffic behind a narrow proxy contract rather than letting the agent
talk directly to the full provider API. Also close the audit's port-80 finding and
block link-local/RFC1918 metadata ranges.

**C3 — Drop root and capabilities.**  
Status: **implemented and verified for agent execs.** guestd drops the child's
real uid/gid to 1001 before execing bubblewrap, and bubblewrap drops all
capabilities. Keep this as an invariant for every non-operator execution path.

**C4 — Immutable rootfs and fixed system permissions.**  
Status: **implemented and verified.** The root disk is read-only and image
population now happens in a Linux imager container so Unix ownership and modes
survive. Continue to treat writable scratch as explicit and narrow.

**C5 — Add seccomp.**  
Apply a cBPF seccomp profile through bubblewrap. Docker's default profile is a
reasonable floor; then explicitly block dangerous syscalls such as `ptrace`,
`process_vm_readv`, `process_vm_writev`, `kexec_load`, module loading, raw BPF,
and privileged mount operations where Node and the SDK do not need them.

## High

**H1 — Make workspace writes reversible.**  
Egress lockdown protects confidentiality, but it does not protect a user's files
from destructive edits inside the allowed workspace. Add snapshots, copy-on-write
work areas, or explicit checkpoints before risky operations.

**H2 — Gate the irreversible tail.**  
The broker has the `Gate` seam and audit log, but the active implementation still
uses `AllowAll`. Replace it with real per-method/per-door policy. Keep routine
work low-friction, but route irreversible actions such as destructive deletes,
publishing, pushing, sending, or broad egress changes through an `Ask` or `Deny`
decision.

**H3 — Add runtime backstops.**  
Halt or escalate after repeated denied tool calls, unexpected egress attempts, or
high-volume file/network activity. This should be enforced outside the guest so a
compromised process cannot turn it off.

**H4 — Kill raw hardware and kernel surfaces.**  
Enable kernel lockdown where compatible, restrict `/dev/mem`-style access, mount
with `nodev` where possible, and minimize the exposed device model.

**H5 — Add cgroup v2 resource limits.**  
Set memory, CPU, and PID caps for agent children to contain fork bombs, runaway
builds, and accidental memory exhaustion.

**H6 — Hide or isolate foreign session mounts.**  
The product now uses one shared VM with per-session shares under `/sessions/<id>`.
That is efficient, but it means session separation depends on the guest sandbox
and mount permissions. Follow up with one session per VM, per-session mount views,
or read-only/hidden foreign mounts if tenant-style isolation becomes a goal.

**H7 — Forward audit evidence off-guest.**  
Broker audit exists on the host side. Add guest-side process/audit events only if
they are forwarded out of the VM, because a compromised guest can tamper with
local-only logs.

## Medium

- Set `kernel.kptr_restrict=2`, `kernel.dmesg_restrict=1`, and
  `kernel.unprivileged_bpf_disabled=1`.
- Lock module loading after boot if no later dynamic module load is needed.
- Treat tool-readable content as untrusted. Files, web pages, repo text, and tool
  errors can all carry prompt injection.
- Avoid rendering attacker-controlled markdown images or remote links in privileged
  UI contexts.

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
