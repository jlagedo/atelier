# Atelier

Monorepo for a Cowork-style desktop AI workspace: a **Go** host service drives a Linux utility VM on Windows HCS, a **TypeScript** agent loop runs the AI, and an **Electron/React** app is the UI.

> Full design, decisions, and a glossary live in **[`docs/design.md`](docs/design.md)**.

## Layout
| Dir | What |
|---|---|
| `apps/` | TS deployables — the Electron desktop app |
| `packages/` | Shared TS libs — agent loop, protocol, provider seam, UI |
| `services/` | One Go module — host broker, in-VM daemon, dev CLI |
| `image/` | VM image build — kernel + initrd + rootfs bundle |
| `skills/` | Skill distribution (DXT/`.mcpb` analog) |
| `tools/` | Repo tooling (codegen, scripts) |
| `docs/` | Design & architecture docs |

Each folder has a `README.md` reminder of its purpose.

**Status:** barebones structure only — no project scaffolding yet.
