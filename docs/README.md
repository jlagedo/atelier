# Atelier Docs

Purpose: help engineers find the current implementation map, security register,
status trackers, and historical research without reading stale plan text first.

Primary reader: engineers changing the desktop app, broker, VM image, or in-guest
agent.

## Open By Task

| Task | Read |
| --- | --- |
| Understand the live UI -> broker -> VM -> agent path | [`architecture/runtime-architecture.md`](architecture/runtime-architecture.md) |
| Find historical rationale | [`architecture/design.md`](architecture/design.md) |
| Review VM sandbox risks | [`security/vm-sandbox.md`](security/vm-sandbox.md) |
| Cross-check the cage vs. an external escape benchmark | [`security/sandbox-escape-benchmark-2026.md`](security/sandbox-escape-benchmark-2026.md) |
| Harden App-to-Broker IPC | [`security/ipc-security.md`](security/ipc-security.md) |
| Check implementation history | [`status/implementation-status.md`](status/implementation-status.md) |
| Continue the macOS port | [`plans/macos-port-execution.md`](plans/macos-port-execution.md), then [`plans/macos-port-plan.md`](plans/macos-port-plan.md) |
| Continue the OpenHands/partisan cutover | [`plans/openhands-adoption.md`](plans/openhands-adoption.md) |
| Build Windows virtio-fs sharing for an EL10 guest (HDV) | [`plans/windows-virtiofs-hdv.md`](plans/windows-virtiofs-hdv.md), with [`research/rocky-el10-migration.md`](research/rocky-el10-migration.md) |
| Evaluate package cache overlays | [`plans/package-cache-overlay.md`](plans/package-cache-overlay.md) |
| Change desktop color tokens | [`ui/color-system.md`](ui/color-system.md); preview in [`ui/color-mock.html`](ui/color-mock.html) |
| Continue the agent-interaction / canvas UX rethink | [`design/agent-interaction-paradigm.md`](design/agent-interaction-paradigm.md) |
| Review docs staleness and cleanup notes | [`DOC_AUDIT.md`](DOC_AUDIT.md) |

## Document Status

| Status | Documents |
| --- | --- |
| Current reference | `architecture/runtime-architecture.md`, `security/vm-sandbox.md`, `security/ipc-security.md`, `ui/color-system.md` |
| Active tracker | `plans/macos-port-execution.md`, `plans/openhands-adoption.md` |
| Proposal | `plans/package-cache-overlay.md`, `plans/windows-virtiofs-hdv.md`, `design/agent-interaction-paradigm.md` |
| Historical log | `architecture/design.md`, `status/implementation-status.md`, `plans/macos-port-plan.md` |
| Research snapshot | `research/claude-cowork-internals.md`, `research/rocky-el10-migration.md`, `security/sandbox-escape-benchmark-2026.md` |
| Raw evidence | `security/audits/2026-05-24-vz-guest-assessment.md` |

Build, run, and test commands live in [`../README.md`](../README.md) and
[`../CLAUDE.md`](../CLAUDE.md).
