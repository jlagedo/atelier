# Atelier Docs

Start here when the repo shape or history feels fuzzy. Each document has one job:

| Document | Purpose |
|---|---|
| [`design.md`](design.md) | Product/architecture rationale, major decisions, glossary. Mostly historical, but kept current where decisions changed. |
| [`runtime-architecture.md`](runtime-architecture.md) | Concrete process, protocol, port, and source-file map from renderer to in-guest agent. |
| [`implementation-status.md`](implementation-status.md) | Slice-by-slice implementation history and current milestone status. |
| [`ipc-security.md`](ipc-security.md) | Hop 2 App-to-Broker security gap and hardening ladder. |
| [`vm-security-assessment.md`](vm-security-assessment.md) | Internal VM audit findings plus remediation status from the live Hyper-V verification. |
| [`vm-hardening.md`](vm-hardening.md) | Forward-looking VM hardening roadmap after the audit fixes. |

Operational build/run/test commands live in the root [`README`](../README) and
[`AGENTS.md`](../AGENTS.md).
