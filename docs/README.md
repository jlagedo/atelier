# Atelier Docs

Start here when the repo shape or history feels fuzzy. Each document has one job:

| Document | Purpose |
|---|---|
| [`design.md`](design.md) | Product/architecture rationale, major decisions, glossary. Mostly historical, but kept current where decisions changed. |
| [`runtime-architecture.md`](runtime-architecture.md) | Concrete process, protocol, port, and source-file map from renderer to in-guest agent. |
| [`implementation-status.md`](implementation-status.md) | Slice-by-slice implementation history and current milestone status. |
| [`macos-port-plan.md`](macos-port-plan.md) | Planning notes and proposed structure for adding a macOS Virtualization.framework backend. |
| [`macos-port-execution.md`](macos-port-execution.md) | Execution tracker for the macOS port: thin reviewable slices, acceptance criteria, and a progress dashboard. |
| [`ipc-security.md`](ipc-security.md) | Hop 2 App-to-Broker security gap and hardening ladder. |
| [`vm-security-assessment.md`](vm-security-assessment.md) | Internal VM audit findings plus remediation status from the live Hyper-V verification. |
| [`vm-hardening.md`](vm-hardening.md) | Forward-looking VM hardening roadmap after the audit fixes. |
| [`security.md`](security.md) | Consolidated VM + IPC security notes (audit findings, hardening, threat model). |
| [`openhands-adoption.md`](openhands-adoption.md) | Review + phased plan for replacing the in-guest agent with an OpenHands-SDK engine (provider-agnostic, WORK now, expand later). |
| [`package-cache-overlay.md`](package-cache-overlay.md) | Design for fast + persistent + isolated `pip`/`uv`/`npm` installs via a shared read-only cache (overlayfs lower) + per-session writeable upper. |

Operational build/run/test commands live in the root [`README`](../README) and
[`AGENTS.md`](../AGENTS.md).
