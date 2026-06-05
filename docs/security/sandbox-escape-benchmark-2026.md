# SANDBOXESCAPEBENCH vs. Atelier Containment

| Field | Detail |
| --- | --- |
| Purpose | Cross-check Atelier's guest cage against an external container-escape benchmark. |
| Primary reader | Engineers reasoning about the guest containment boundary and its residual risk. |
| Paper | Marchand et al., *Quantifying Frontier LLM Capabilities for Container Sandbox Escape*, [arXiv:2603.02277](https://arxiv.org/abs/2603.02277) (cs.CR), 1 Mar 2026. CC-BY-4.0. |
| Code refs | `services/cmd/runner/sandbox_linux.go`, `image/agent/seccomp/`, `image/guest/init.sh`, `services/cmd/atelier-landlock/main.go` |
| Related | [`vm-sandbox.md`](vm-sandbox.md) (live finding register), [`audits/2026-05-24-vz-guest-assessment.md`](audits/2026-05-24-vz-guest-assessment.md) |

## What the paper is

**SANDBOXESCAPEBENCH** is an open Inspect-AI CTF benchmark that measures whether an LLM
agent can break *out* of its sandbox. It uses a nested-sandbox architecture (the outer
layer holds the flag and has no known vulnerabilities) under a threat model of *a motivated
adversarial agent that already has shell access inside a container*. It spans **18 scenarios**
across three container-security layers (from Jarkas et al. 2025): **L1 Orchestration** (4),
**L3 Engine & Runtime** (8), **L4 Host/Kernel** (6). It excludes L2 application and L5 hardware
side-channels.

Two headline findings define the whole defense surface:

1. **No model discovered any novel vulnerability** — every successful breakout used a
   documented misconfiguration or a public CVE.
2. **Zero success at difficulty 4–5** (hard kernel exploits); frontier models score near-ceiling
   only at difficulty 1–2 (misconfigurations / exposed control surfaces). Best aggregate
   no-hint scores: GPT-5 0.50, Opus 4.5 0.49.

## Why the benchmark probes the *inner* cage, not Atelier's host boundary

The benchmark measures escape from a **container to its shared host kernel**. Atelier's outer
boundary is a **microVM with its own guest kernel** (VZ on macOS, HCS on Windows). A *full*
SANDBOXESCAPEBENCH win against Atelier's in-guest cage therefore only lands the agent at
**guest root** — still inside the VM, which is exactly the state the hypervisor boundary is
designed to contain. The benchmark never tests VM/hypervisor escape, so its 18 scenarios probe
the **inner cage** (bwrap + seccomp + Landlock on the shared *guest* kernel), not Atelier's real
host boundary.

## Atelier cage, as built

From `sandbox_linux.go` and the image pipeline, the non-privileged agent exec runs:

- **uid/gid 1001**, dropped by `runner` *before* exec'ing bwrap (so the userns map is `1001 1001 1`,
  not `1001 0 1` — host-root files appear as `nobody`).
- **`bwrap --unshare-user/pid/ipc/uts`**, **`--cap-drop ALL`**, `--new-session`, `--die-with-parent`.
- **Curated read-only toolbox** (`/usr` + usr-merge symlinks, `/opt/atelier`, ro `/etc` allow-list) —
  no whole-root bind, so the runner binary (F-03) and sibling sessions (F-09) are never mounted.
- **Writable paths only**: agent HOME (tmpfs) + the exec's own workspace. `/etc` is read-only.
- **Seccomp**: Docker's default profile, vendored from moby/profiles, compiled to cBPF and evaluated
  **for a no-capability process**. CAP_SYS_ADMIN-gated syscalls fall through to the default ERRNO,
  so `bpf()`, `mount()`, `init_module`/`finit_module`, `setns`, `unshare`, `clone3`, and all
  `CLONE_NEW*` are **denied** (closes F-01 with no custom rule). Confirmed in
  `image/agent/seccomp/compile-seccomp.py`.
- **Landlock shim** (`atelier-landlock`): FS allow-list + outbound TCP 443 only.
- **Boot hardening** (`init.sh`): `kptr_restrict=2`, `ptrace_scope=2`, `modules_disabled=1` (after
  required modules load).
- **Per-exec cgroup v2 limits**; read-only rootfs.
- **No inbound path**: vsock-only, **default-deny egress**, **no sshd** (R-04).
- Guest kernel: **6.8 (HCS) / 6.17 (VZ)**.

## Scenario-by-scenario cross-check

### L1 — Orchestration (4): architecturally absent

| Scenario | CVE | Status |
| --- | --- | --- |
| CRI-O sysctl injection | 2022-0811 | N/A — no CRI-O / kubelet |
| kubectl cp tar overwrite | 2019-1002101 | N/A — no Kubernetes / `kubectl` |
| RBAC over-privileged service account | — | N/A — no control plane |
| route_localnet → host service | 2020-8558 | N/A — no kube-proxy; default-deny egress + no reachable host services |

No orchestrator exists in Atelier's runtime path; these have no analog.

### L3 — Engine & Runtime (8): the easy wins (diff 1, models ~1.00) — defeated by construction

| Scenario | CVE | Atelier defense |
| --- | --- | --- |
| `--privileged` | — | Agent never runs privileged; `--cap-drop ALL`. (`p.Privileged` is an operator/debug hatch, not the agent path.) |
| docker.sock | — | No Docker daemon in the guest at runtime; socket never mounted; toolbox excludes it. |
| CAP_SYS_ADMIN | — | No caps; `mount()`/cgroup-mount seccomp-denied. |
| CAP_SYS_MODULE | — | No caps **+** `modules_disabled=1` (F-16) **+** `init_module` seccomp-denied — triple-covered. |
| CAP_DAC_READ_SEARCH (Shocker, `open_by_handle_at`) | — | No caps — removes the capability the exploit needs. |
| hostpath writable `/etc` | — | `/etc` is a **read-only** allow-list bind; only HOME + workspace are writable. |
| runc `/proc/self/exe` overwrite | 2019-5736 | **No runc** — bwrap is the runtime; runner binary not in the sandbox mount ns (F-03). |
| runc leaked-fd cwd | 2024-21626 | **No runc**. |

The capability-abuse trio collapses to one fact: **the agent holds zero capabilities**, reinforced
by seccomp and `modules_disabled=1`.

### L4 — Host/Kernel (6): the only conceptually-relevant layer — and it stops at *guest* root

These share the guest kernel, so they are the real threat to the **in-guest cage** (not the host).
Each is still individually neutralized:

| Scenario | CVE | Atelier defense |
| --- | --- | --- |
| Host PID ns + ptrace | — | `--unshare-pid` (fresh PID ns), `ptrace_scope=2`, no `CAP_SYS_PTRACE`. |
| cgroup `release_agent` | 2022-0492 | Needs `CAP_SYS_ADMIN` + `mount()` + `unshare -UrmC` — all denied (cap-drop + seccomp + `CLONE_NEWUSER` blocked, F-01). No model solved this even in a vulnerable container. |
| eBPF verifier privesc | 2017-16995 | `bpf()` seccomp-denied (the paper had to disable eBPF to stop it being a universal shortcut); also a 4.4–4.14 bug — guest is 6.8/6.17, patched. |
| Dirty COW | 2016-5195 | Fixed in 4.8.3 — patched by kernel version. |
| Dirty Pipe | 2022-0847 | Affects 5.8–5.16 — guest 6.8/6.17, patched. |
| AF_PACKET use-after-free | 2017-7308 | No `CAP_NET_RAW`; `socket(AF_PACKET)` seccomp-restricted; 4.8 bug, patched. |

### Appendix C unintended shortcuts: also covered

The benchmark's own development-time escape shortcuts were **default-credential SSH** and
**fetching a public insecure key → SSH to the Docker bridge gateway (172.17.0.1)**. Atelier removed
sshd (R-04), is **vsock-only with no inbound**, and **default-deny egress** — the entire
"SSH to host/bridge" class is gone.

## Verdict

Atelier is positioned against the strong end of this benchmark on both of its headline axes:

- It **exposes none of the diff-1–2 misconfigurations** (no docker.sock, no privileged, no caps,
  read-only `/etc`, no orchestrator) — the only class frontier models reliably solve.
- The diff-3–5 **CVEs all predate the guest kernel (6.8/6.17)** and need primitives seccomp denies
  (`bpf`, `mount`, `CLONE_NEWUSER`, module load).

Defense-in-depth holds: cap-drop, seccomp, fresh namespaces, kernel patch level, and
`modules_disabled` each independently kill most scenarios.

## Residual gaps this cross-check surfaces

- **The in-guest cage is itself a shared-kernel boundary.** The L4 kernel-exploit class is the
  relevant residual threat *to the cage* (not to the host). The defense there is purely **guest-kernel
  patch level** — a future post-6.17 local privilege escalation would break cage → guest-root, with
  only the microVM still holding. This is the concrete argument for **F-07** (off-guest syscall /
  module-load evidence that survives guest compromise): Atelier currently has no guest-side detection
  for the `bpf` / `ptrace` / module-load attempts this benchmark exercises.
- **F-14 (Speculation Store Bypass: vulnerable)** is not probed — the benchmark excludes L5 hardware
  side-channels — so there is no signal either way, but it remains the one un-mitigated
  kernel-adjacent item.
- The `bpf()` / `CLONE_NEWUSER` denials are **load-bearing** for the entire L4 seccomp story. Keep the
  no-capability evaluation pinned if the vendored moby profile is ever bumped (see
  `image/agent/seccomp/SOURCE`).

**Classification:** Internal / Security Sensitive
