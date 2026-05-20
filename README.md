# Atelier

A Cowork-style **desktop AI workspace**: a **Go** host service drives a Linux utility VM on
Windows HCS, a **TypeScript** agent loop runs the AI, and an **Electron/React** app is the UI.
The whole point is letting an AI agent work on **local files** safely — by containment, not
per-click consent.

> Full design, decisions, and a glossary live in **[`docs/design.md`](docs/design.md)**.
> Build/run/test instructions and conventions live in **[`AGENTS.md`](AGENTS.md)**.

## Status

Early scaffold — two areas are runnable, the rest follow the milestone ladder in the design doc:

- **Desktop shell** (`apps/desktop`) — a hardened Electron + React 19 + Tailwind v4 app booting
  a chat-forward **mock** layout, with a working IPC seam. No real agent yet.
- **Host service** (`services`) — a Go broker speaking JSON-RPC 2.0 (Content-Length framed) over
  a named pipe (Windows) / unix socket (dev); `getStatus` works end-to-end, capability methods
  are policy-gated, audited stubs. HCS (hcsshim) + guest transport are next.

## Layout

| Dir | What |
|---|---|
| `apps/desktop` | Electron/React desktop UI (chat-forward shell) |
| `services` | Go module — privileged host broker, in-VM daemon, dev CLI |
| `packages` | Shared TS libs — agent loop, protocol, provider seam, UI *(skeleton)* |
| `image` | VM image build pipeline — kernel + initrd + rootfs bundle |
| `skills` | Skill distribution (DXT/`.mcpb` analog) *(skeleton)* |
| `tools/protogen` | Protocol codegen — schema → TS + Go |
| `docs` | Design & architecture docs |

## Quick start

```sh
# Desktop app (TypeScript / Electron)
cd apps/desktop && npm install && npm start     # headless: xvfb-run -a npm start

# Host service (Go) — dev end-to-end over a unix socket
cd services
go run ./cmd/host  -addr /tmp/atelier-host.sock &
go run ./cmd/vmctl -addr /tmp/atelier-host.sock getStatus
```

See **[`AGENTS.md`](AGENTS.md)** for the full command set, conventions, and verification notes.

## Architecture at a glance

```
Renderer (React, sandboxed)
   │  Hop 1: Electron IPC
Main process (Node) ── conductor
   │  Hop 2: named pipe / unix socket — JSON-RPC 2.0 (Content-Length)
Go host service ── broker (policy + audit) + HCS driver  ·  privileged
   │  Hop 3: hvsocket — control / exec / file planes
Linux utility VM ── kernel + rootfs + python + tools
```

> Versions: this scaffold uses latest-stable libraries, which diverges from the design doc's
> Cowork version pins (§11). See `AGENTS.md`.
