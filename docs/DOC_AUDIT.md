# Docs Audit

Purpose: record doc status, staleness risks, cleanup decisions, and claims that
need author confirmation.

Primary reader: engineers deciding which doc to trust before changing code.

## Reviewed Set

| Bucket | Files |
| --- | --- |
| Current reference | `architecture/runtime-architecture.md`, `security/vm-sandbox.md`, `security/ipc-security.md`, `ui/color-system.md` |
| Active trackers | `plans/macos-port-execution.md`, `plans/openhands-adoption.md` |
| Historical logs | `architecture/design.md`, `status/implementation-status.md`, `plans/macos-port-plan.md` |
| Proposals | `plans/package-cache-overlay.md` |
| Research snapshots | `research/claude-cowork-internals.md`, `research/rocky-el10-migration.md` |
| Raw evidence | `security/audits/2026-05-24-vz-guest-assessment.md` |

## Review Flags

| Document | Flag |
| --- | --- |
| `architecture/design.md` | Historical. It preserves Windows/HCS and TypeScript-agent rationale, not the current runtime map. |
| `status/implementation-status.md` | Historical through S6.1. Use the top overlay for current state; use body sections as slice history. |
| `plans/macos-port-plan.md` | Mixed active and historical plan text. Early sections still mention artisan/TypeScript because they predate partisan. |
| `plans/macos-port-execution.md` | Active tracker. S8 and S10 remain open; S9 unblocks the final desktop agent-turn re-run. |
| `plans/openhands-adoption.md` | Active tracker. Phase 4 conformance and Node/artisan removal remain open. |
| `plans/package-cache-overlay.md` | Proposal only. No implementation has landed. |
| `security/audits/2026-05-24-vz-guest-assessment.md` | Raw audit. It contains artisan-era process paths; use `security/vm-sandbox.md` as the current register. |
| `research/claude-cowork-internals.md` | Point-in-time reverse-engineering snapshot from January-April 2026. Anthropic internals can drift. |
| `research/rocky-el10-migration.md` | Research record. No Rocky/EL10 migration code has landed. |
| `ui/color-mock.html` | Static preview. The source of truth is `apps/desktop/src/renderer/index.css`. |

## Cleanup Log

| Change | Reason |
| --- | --- |
| Rewrote `docs/README.md` around tasks and document status. | Engineers need a routing table before background. |
| Kept raw audits and research snapshots intact. | Rewriting evidence-heavy files risks changing point-in-time facts. |
| Labeled historical docs at the top. | Prevent stale plan text from reading as current implementation. |
| Removed missing-doc references from the docs index during the reorganization. | The old index linked to deleted `vm-security-assessment.md` and `vm-hardening.md`. |
| Consolidated VM sandbox security into `security/vm-sandbox.md`. | Older `security.md` contradicted the 2026-05-24 audit on F-03, F-04, F-06, F-09, and F-16. |
| Rewrote `architecture/runtime-architecture.md` around partisan/OpenHands, VZ/HCS, macOS virtio-fs, Windows 9p, and known security gaps. | The prior runtime doc described the old TypeScript agent and Windows-only path as current. |

## Product Gaps To Keep Visible

These are product/security gaps, not doc bugs.

| Gap | Source |
| --- | --- |
| Provider key still enters the guest process environment. | `security/vm-sandbox.md` F-02 |
| Broker policy gate is still `AllowAll`. | `security/vm-sandbox.md` F-10; `security/ipc-security.md` |
| Hop 2 pipe/socket access control is not ship-grade. | `security/ipc-security.md` |
| macOS packaging/notarization remains open. | `plans/macos-port-execution.md` S10 |
| Full desktop agent-turn re-run on macOS remains open. | `plans/macos-port-execution.md` S8 |
| Partisan conformance and Node/artisan removal remain open. | `plans/openhands-adoption.md` Phase 4 |

## Claims To Confirm

| Claim | Why confirm |
| --- | --- |
| `research/claude-cowork-internals.md` symbol names, flags, codenames, and product behavior. | Source is reverse-engineering from January-April 2026; Anthropic ships frequently. |
| `research/rocky-el10-migration.md`: EL10 lacks 9p support. | The doc confirms EL9/Rocky 9 and treats EL10 as a working assumption pending a spike. |
| `security/vm-sandbox.md` F-15 exact file paths under partisan. | The finding came from the artisan/Claude-agent path and needs a partisan home-directory re-audit. |
| `plans/package-cache-overlay.md` implementation details. | The design is validated against docs and issue trackers, but not implemented in Atelier. |
