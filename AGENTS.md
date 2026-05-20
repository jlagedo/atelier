# AGENTS.md

Contributor + agent guide for **Atelier** — a Cowork-style desktop AI workspace: a Go host
service drives a Linux utility VM on Windows HCS, a TypeScript agent loop runs the AI, and an
Electron/React app is the UI. Full design, decisions, and glossary: [`docs/design.md`](docs/design.md).

This file is the source of truth for how to build, run, test, and what conventions to follow.
Per-directory placeholder READMEs were removed from initialized areas in favor of this file.

## Repo layout

| Dir | What | State |
|---|---|---|
| `apps/desktop` | Electron/React desktop UI (the shell) | scaffolded (feature 0) |
| `services` | One Go module — host broker, in-VM daemon, dev CLI | scaffolded (Hop-2 seam) |
| `packages` | Shared TS libs — agent loop, protocol, provider seam, UI | skeleton |
| `image` | VM image build — kernel + initrd + rootfs bundle | scaffolded (build pipeline) |
| `skills` | Skill distribution (DXT/`.mcpb` analog) | skeleton |
| `tools/protogen` | Protocol codegen (schema → TS + Go) | scaffolded |
| `docs` | Design & architecture docs | `design.md` |

Generated/build output is gitignored: `apps/desktop/.vite`, `apps/desktop/out`, `**/node_modules`,
`packages/protocol/src`, `services/pkg/protocol`, Go binaries.

## Desktop app — `apps/desktop` (TypeScript / Electron)

Stack: Electron Forge + `@electron-forge/plugin-vite`, Vite, React 19, TypeScript, Tailwind v4,
oxlint/oxfmt, vitest.

```sh
cd apps/desktop
npm install
npm start            # dev; headless box: xvfb-run -a npm start
npm run typecheck    # tsc --noEmit
npm run lint         # oxlint
npm run format       # oxfmt (code only)
npm test             # vitest
npm run package      # full Forge build (no window)
```

Conventions:
- Renderer is hardened (design §2): `sandbox: true`, `contextIsolation: true`,
  `nodeIntegration: false`, strict CSP (`src/main/security.ts` — dev-relaxed for HMR, prod-strict).
- The renderer's only bridge is a narrow `contextBridge` (`window.atelier`) in `src/preload`.
- IPC channel names are centralized in `src/main/ipc/channels.ts` (shared by main + preload).
- Tailwind v4: no `postcss.config`/`tailwind.config`; wired via `@tailwindcss/vite` +
  `@import "tailwindcss"` / `@plugin` in `src/renderer/index.css`.

## Host services — `services` (Go)

Module: `github.com/jlagedo/atelier/services`. Protocol (Hop 2, design §8): JSON-RPC 2.0 with
Content-Length framing, over a named pipe on Windows / a unix socket for dev.

```sh
cd services
go build ./... && go test ./... && go vet ./... && gofmt -l .
GOOS=windows go build ./...     # verify the Windows named-pipe / HCS paths compile

# dev end-to-end (unix socket):
go run ./cmd/host  -addr /tmp/atelier-host.sock &
go run ./cmd/vmctl -addr /tmp/atelier-host.sock getStatus
```

Conventions:
- Windows-only code lives behind `//go:build windows` with a `!windows` stub sibling
  (e.g. `internal/rpc/transport_*.go`, `internal/hcs/hcs_*.go`) so `go build ./...` works on Linux.
- `internal/broker` is the containment chokepoint: every capability use passes the policy gate
  (allow/ask/deny) + audit log before acting (design §10).
- `go.mod` `go` directive is pinned to the installed toolchain (1.24); latest stable is Go 1.26.

## Protocol codegen — `tools/protogen`

`packages/protocol/schema/protocol.json` is the **canonical** Hop-2 protocol (design §8).
`tools/protogen` (zero-dep Node) generates TS + Go from it; outputs are **gitignored** —
regenerate, don't hand-edit.

```sh
npm run protogen        # from repo root
# writes packages/protocol/src/index.ts  and  services/pkg/protocol/protocol.go
```

TODO: emit Zod schemas alongside the TS interfaces.

## VM image build — `image/`

Builds the utility-VM bundle (kernel + initrd + ext4 rootfs VHD), Cowork's
`claudevm.bundle` analog (design §7). Sources are tracked under
`image/{rootfs,initrd,kernel,guest}`; build output goes to `image/bundle/` (gitignored).
Big artifacts (multi-GB VHDs) are **not** committed — they're produced here and stored
externally (registry / release assets), not in git/LFS.

```sh
cd image
./build.sh check        # tool readiness (docker, mke2fs, qemu-img)
./build.sh rootfs       # docker export ubuntu:22.04 -> ext4 (mke2fs -d, no root) -> VHD
./build.sh all          # kernel (TODO M1) + rootfs + initrd (TODO M1) + bundle
```

## Versions

This scaffold deliberately uses **latest stable** libraries, diverging from `docs/design.md`
§11's Cowork pins (Tailwind 3.4 → 4, React 18 → 19, Electron 41 → 42, etc.). Divergences are
documented inline where they matter.

## Environment & verification

The dev environment is **headless Linux**; the desktop's real target is **Windows**.
- TS: verify with typecheck + lint + vitest + `package`; use `xvfb-run` to boot a real window.
- Go: verify with build + test, plus a `GOOS=windows` cross-compile for the Windows paths.
- State clearly when something can't be verified here (no display, restricted network) rather
  than claiming success.

## Housekeeping

- Don't commit build output or generated code (already gitignored).
- Comments explain WHY, not WHAT; keep them minimal.
- Commit messages: conventional style (`feat`/`fix`/`chore` + scope), focused on the why.
