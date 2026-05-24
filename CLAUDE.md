# CLAUDE.md

Contributor + agent guide for **Atelier** — a Cowork-style desktop AI workspace: a Go host
service drives a Linux utility VM (VZ on macOS, HCS on Windows), a TypeScript agent loop runs
the AI *inside* that VM (Topology B), and an Electron/React app is the UI. The point is letting an AI agent work
on local files safely by **containment** (the VM is the cage), not per-click consent. Full design,
decisions, and glossary: [`docs/design.md`](docs/design.md); slice-by-slice implementation status:
[`docs/implementation-status.md`](docs/implementation-status.md); the end-to-end run guide is the root
[`README`](README).

This file is the source of truth for how to build, run, test, and what conventions to follow.

## Working efficiently in this repo

This repo is small (~175 files) but its docs are large. Keep the main context lean and fast:
- For any "where is X / how does Y work / which files touch Z" question, use the **Explore**
  subagent (or a general-purpose Agent) instead of grepping and reading inline. Have it return
  conclusions + `file:line`, not file dumps — exploration then stays out of the main context.
- Do **not** reflexively open the big design docs. The repo-layout table and the "Where things
  live" map below, plus the relevant source file, are usually enough. Only read `docs/design.md`,
  `docs/implementation-status.md`, `docs/claude-cowork-internals.md`, and the other multi-hundred-line
  docs when a task genuinely needs that depth — and read the relevant section, not the whole file.

## Repo layout

| Dir | What | State |
|---|---|---|
| `apps/desktop` | Electron/React desktop UI (the shell) | WORK mode wired to the broker; chat mode mock |
| `services` | One Go module — host broker (`host`), in-VM daemon (`guestd`), dev CLI (`vmctl`) | full substrate (boot/exec/files/net) |
| `packages/agent` | The Claude Agent SDK loop — host (`cli.ts`) and in-guest (`cli-guest.ts`) | both topologies; in-guest is the live path |
| `packages/provider` | Provider seam — resolves model + env for the loop | Anthropic API now, Eliza later |
| `packages/protocol` | Generated Hop-2 protocol bindings (schema is canonical) | generated, gitignored |
| `image` | VM image build — kernel + initrd + rootfs bundle; bakes in the agent | build pipeline |
| `tools/protogen` | Protocol codegen (schema → TS + Go) | working |
| `docs` | Design, runtime architecture, implementation, and security docs | see `docs/README.md` |

Generated/build output is gitignored: `build/` (the orchestrator's staged artifacts),
`apps/desktop/.vite`, `apps/desktop/out`, `**/node_modules`, `packages/protocol/src`,
`services/pkg/protocol`, `services/bin`, `image/.work`, `image/bundle`.

### Where things live (jump here, don't search)

| To touch… | Go to |
|---|---|
| Policy gate / containment chokepoint | `services/internal/broker/broker.go` |
| Files door (workspace path jailing) | `services/internal/broker/files.go` |
| macOS VZ driver | `services/internal/vmm/driver_darwin.go` |
| VM lifecycle | `services/internal/vmm/manager.go` |
| Egress jail (default-deny network) | `services/internal/netjail/network.go` |
| Windows HCS bindings | `services/internal/hcs/computecore_windows.go` |
| Session Manager (host state machine) | `apps/desktop/src/main/sessions/manager.ts` |
| Hop-2 named-pipe JSON-RPC client | `apps/desktop/src/main/host-client/client.ts` |
| In-guest agent loop (live path) | `packages/agent/src/cli-guest.ts` |
| Host agent loop + broker tools | `packages/agent/src/cli.ts`, `packages/agent/src/broker/client.ts` |
| Protocol (canonical schema) | `packages/protocol/schema/protocol.json` |

## Build the whole stack

`scripts/build-all.mjs` is the **single source of truth** for the build. One command builds +
verifies everything from zero and writes **every artifact into one tree**, `build/<config>/`:

```sh
npm run build:all                      # debug (default): clean + build all + verify -> build/debug/
npm run build:all -- --config=release  # stripped Go + self-contained               -> build/release/
npm run build:all -- --only=host       # one phase: protocol + host/vmctl (codesigned on macOS)
npm run build:all -- --only=image      # one phase: VM image bundle
npm run build:all -- --only=desktop    # one phase: packaged desktop app
npm run build:all -- --deep            # true from-zero: also wipe node_modules + image/.work
npm run build:all -- --no-verify       # skip tests/typecheck/lint
npm run build:all -- --skip-image      # fast host-only iteration (skip the Docker image)
```

`build/<config>/` layout: `host(.exe)` + `vmctl(.exe)` (Go broker + dev CLI, broker codesigned on
macOS), `image/<target>/` (the VM bundle), `desktop/` (packaged Electron app).

The orchestrator (zero-dep Node, runs on both OSes) drives the chain in order — submodule → clean →
protogen → host build (cgo + codesign, done **in-process**, no per-OS shell script) → VM image →
desktop → verify — branching only for the irreducible platform bits: `codesign` on macOS (VZ refuses
an unsigned broker) and the `wsl` prefix for the Docker image build on Windows (unverified from a
Mac). `guestd` is cross-compiled into the rootfs by the image build (not a host binary); verify also
linux-cross-compiles it.

The image build lives in `image/build.sh` — one cross-OS bash+Docker script (native on macOS, via
`wsl` on Windows). It writes to `image/bundle/<target>/` by default; the orchestrator redirects it
into `build/<config>/image/` via `ATELIER_OUT_BASE`. Generated source (`packages/protocol/src`,
`services/pkg/protocol`) is imported by module path, so it stays in-tree (regenerated by `protogen`,
not moved into `build/`). All of `build/` is gitignored.

Then run the broker (`build/<config>/host`; elevated only on Windows) and the app
(`ATELIER_BUNDLE_DIR=build/<config>/image/<target> npm run dev`). See the root [`README`](README) for
the full run guide + the `vmctl` terminal path + dev-without-VM.

## Desktop app — `apps/desktop` (TypeScript / Electron)

Stack: Electron Forge + `@electron-forge/plugin-vite`, Vite, React 19, TypeScript, Tailwind v4,
shadcn/ui (Radix + cva + tailwind-merge), `react-markdown`/`remark-gfm`, Phosphor icons, IBM Plex
fonts, oxlint/oxfmt, vitest.

```sh
cd apps/desktop
npm install
npm start            # dev
npm run typecheck    # tsc --noEmit
npm run lint         # oxlint
npm run format       # oxfmt (code only)
npm test             # vitest
npm run package      # full Forge build (no window) -> apps/desktop/out/
```

`package` needs the `yauzl@^3.3.1` override in `package.json`: `electron-forge`'s `extract-zip@2.0.1`
pins `yauzl@2.10.0`, whose inflate stream deadlocks on large entries under Node 24+, making
`electron-forge package` silently exit 0 with no `out/`. The override is still required on every
upgrade path (even `@electron/packager@20` pins `extract-zip@2`).

Process layout:
- `src/main` — Node main process. `host-client/` is the Hop-2 named-pipe JSON-RPC client to the Go
  broker; `sessions/` is the **Session Manager** (`manager.ts`) + durable `store.ts` — the
  host-owned state machine that brings up `vm0` once and runs **concurrent persistent per-session
  in-guest loops** (`cli-guest --serve`), with idle/LRU **hibernate→resume** to bound guest memory;
  `workspace/` reads + watches the session folder to mirror deliverables back to the UI.
- `src/renderer` — sandboxed React. `features/{chat,sessions,workspace}` (chat view + composer,
  session list/mode/status, file panel), `components/ui` (shadcn primitives).

Conventions:
- Renderer is hardened (design §2): `sandbox: true`, `contextIsolation: true`,
  `nodeIntegration: false`, strict CSP (`src/main/security.ts` — dev-relaxed for HMR, prod-strict).
- The renderer's only bridge is a narrow `contextBridge` (`window.atelier`) in `src/preload`.
- IPC channel names are centralized in `src/main/ipc/channels.ts` (shared by main + preload).
- Tailwind v4: no `postcss.config`/`tailwind.config`; wired via `@tailwindcss/vite` +
  `@import "tailwindcss"` / `@plugin` in `src/renderer/index.css`.
- WORK mode drives the real broker; chat mode is still mock (`renderer/lib/mock-data.ts`).
- Env knobs: `ATELIER_BUNDLE_DIR` (per-target bundle dir, e.g. `image\bundle\windows-amd64-hyperv`; platform-aware default resolver lands in S3), `ATELIER_IDLE_MS` (hibernate-after-idle,
  default 10 min), `ATELIER_MAX_ACTIVE` (live loops before LRU hibernate, default 3),
  `ATELIER_BOOT_TIMEOUT_MS` (default 120 000). The model call needs `ANTHROPIC_API_KEY` in the
  environment that launches the app.

## Host services — `services` (Go)

Module: `github.com/jlagedo/atelier/services`. Protocol (Hop 2, design §8): JSON-RPC 2.0 with
Content-Length framing, over a named pipe on Windows / a unix socket for dev. Three binaries under
`cmd/`: **`host`** (the privileged broker), **`guestd`** (the in-VM daemon, cross-compiled into the
rootfs by `image/build.sh`), **`vmctl`** (dev CLI).

```sh
cd services
go build ./... && go test ./... && go vet ./... && gofmt -l .
GOOS=windows go build ./...     # verify the Windows named-pipe / HCS paths compile

# dev end-to-end (unix socket, no VM):
go run ./cmd/host  -addr /tmp/atelier-host.sock &
go run ./cmd/vmctl -addr /tmp/atelier-host.sock getStatus
```

On **macOS (Apple Silicon)** the broker drives Apple's Virtualization.framework via the
`Code-Hex/vz` cgo binding (`internal/vmm/driver_darwin.go`), so darwin builds need
`CGO_ENABLED=1` + Xcode Command Line Tools, and the broker must be codesigned with
`com.apple.security.virtualization` (`services/packaging/darwin/atelier-vm.entitlements`)
under the hardened runtime — the framework refuses to start otherwise, and cgo invalidates
the signature on every rebuild. Build + sign via the orchestrator phase instead of a bare `go build`:

```sh
npm run build:all -- --only=host      # protogen -> cgo build host+vmctl -> codesign host -> build/debug/
build/debug/host  -addr /tmp/atelier-host.sock &
B=build/debug/image/darwin-arm64-vz
build/debug/vmctl -addr /tmp/atelier-host.sock createVM -id vm0 \
  -kernel $B/vmlinuz -initrd $B/initrd -rootfs $B/rootfs.raw
build/debug/vmctl -addr /tmp/atelier-host.sock startVM -id vm0   # serial boot log -> broker stderr
build/debug/vmctl -addr /tmp/atelier-host.sock stopVM  -id vm0
```

End-to-end integration battery (mirrors `build:all` — zero-dep Node, `build/<config>/` tree):

```sh
npm run e2e:host                      # build debug if missing, boot vm0, drive all 11 doors + agent
npm run e2e:host -- --config=release  # against build/release/
npm run e2e:host -- --skip-build      # reuse build/<config>/ as-is (fast-fail if incomplete)
```

`scripts/e2e-host.mjs` spawns the **shipped** broker over a unix socket and exercises every door + the
in-guest agent loop through `vmctl` — the real Hop-2 wire, which the Go unit tests (fake drivers) and
`s7-smoke-darwin.sh` (share shape) don't cover. It splits the two share models into their own sections
(legacy `/workspace` + Files door; concurrent `/sessions/<tag>` — isolation, arbitrary targets,
sibling-safe detach), plus the egress jail (default-deny blocks, allow reaches the model) and
host↔guest bridging both ways. A real boot, so VZ + a codesigned broker + the image bundle are
required; the agent check needs `ANTHROPIC_API_KEY` (it fails the suite if absent).

`internal/` packages: `broker` (policy gate + audit + Files/Network doors), `hcs` (our own
`computecore.dll` bindings + compute-system doc), `vmm` (lifecycle + guest/console wiring), `rpc`
(JSON-RPC codec/transport/notifications), `vsock` (hvsocket dialing), `netjail` (default-deny egress
via gvisor-tap-vsock). The 11 doors live in `pkg/protocol` (generated): `getStatus`, `createVM`,
`startVM`, `stopVM`, `exec`, `execInput`, `attachWorkspace`, `detachWorkspace`, `readFile`,
`writeFile`, `setEgressPolicy`.

Conventions:
- Windows/Linux-only code lives behind `//go:build` tags with a sibling stub
  (e.g. `internal/rpc/transport_*.go`, `internal/hcs/hcs_*.go`, `cmd/guestd/*_linux.go` +
  `*_other.go`) so `go build ./...` works on either host.
- `internal/broker` is the containment chokepoint: every capability use passes the policy gate
  (allow/ask/deny) + audit log before acting (design §10). The Files door is workspace-relative and
  jails paths (rejects `..` and escaping symlinks).
- `go.mod` `go` directive is pinned to the installed toolchain (1.25); latest stable is Go 1.26.

## Agent loop — `packages/agent` (TypeScript)

Hosts `@anthropic-ai/claude-agent-sdk`. Two entry points sharing the same provider + policy seams:
- `cli.ts` — **Topology A** (host loop): the SDK's "hands" are an in-process MCP server whose tools
  route to the broker over Hop 2→3 (`seams/tools.ts`, `broker/client.ts`).
- `cli-guest.ts` — **Topology B** (in-guest loop, the live path): the loop runs *in the cage*, so its
  hands are the SDK's built-in coding tools (Bash/Read/Write/Edit/Glob/Grep) acting directly on the
  guest fs — no broker round-trip for tools; only the model call escapes via the egress jail. Has a
  one-shot mode (`--task`, drives `vmctl agent`) and a persistent `--serve` mode (NDJSON over
  stdin/stdout, driven by the Session Manager; `--resume <id>` for hibernate→resume).

The policy gate (`seams/policy.ts`, wired as the SDK's `canUseTool`) audits **every** tool call in
both topologies. `packages/provider` (`resolveProvider`) picks model + env.

```sh
cd packages/agent
npm install
npm run typecheck    # tsc --noEmit
npm test             # vitest run
npm run dev          # tsx src/cli.ts        (Topology A)
npm run start:guest  # tsx src/cli-guest.ts  (Topology B)
```

The in-guest agent (with its `node_modules` for the target arch — `linux/amd64` on Windows,
`linux/arm64` on macOS) is baked into the rootfs by `image/build.sh`, so the desktop app does
not install or ship it separately.

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

Builds the utility-VM bundle (kernel + initrd + ext4 rootfs VHD), Cowork's `claudevm.bundle`
analog (design §7). Sources are tracked under `image/{rootfs,initrd,kernel,guest}`; build output
goes to `image/bundle/` (gitignored). The matched kernel + `/lib/modules` + boot initramfs all come
from one Ubuntu 22.04 Docker build (so the §7 coupling holds by construction); the same build
cross-compiles `guestd` and bakes the in-guest agent (`stage_context` assembles a small Docker
context from `packages/{agent,provider,protocol}` source and runs `npm install` inside the
target-arch build — `--platform linux/amd64` or `linux/arm64`). Big artifacts (multi-GB VHDs) are
**not** committed — produced here and stored externally, not in git/LFS.

A build `TARGET` (default `windows-amd64-hyperv`) selects guest arch + Docker platform + GOARCH +
disk format + per-target output dir; output goes to `<base>/<target>/`, where `<base>` is `bundle`
by default or `$ATELIER_OUT_BASE` when set (the orchestrator passes `../build/<config>/image`).

```sh
cd image
./build.sh check        # tool readiness + resolved target profile (docker, mke2fs, qemu-img)
./build.sh rootfs       # docker export -> ext4 (mke2fs -d, no root) -> VHD/raw
make all                # Windows: kernel+rootfs+initrd+bundle -> bundle/windows-amd64-hyperv/{vmlinuz,initrd,rootfs.vhd}
make darwin             # macOS arm64 (raw ext4)              -> bundle/darwin-arm64-vz/{vmlinuz,initrd,rootfs.raw}
```

(Standalone runs above write to `image/bundle/<target>/`; `npm run build:all` / `--only=image`
redirects them into `build/<config>/image/<target>/`.)

## Versions

This scaffold deliberately uses **latest stable** libraries, diverging from `docs/design.md`
§11's Cowork pins (Tailwind 3.4 → 4, React 18 → 19, Electron 41 → 42, etc.). Divergences are
documented inline where they matter.

## Library docs — use Context7

Because this stack runs **latest-stable** libraries (see Versions), training data is often
stale here. When you need current API syntax, configuration, setup steps, version-migration
details, or library-specific debugging for any third-party library/framework/SDK/CLI in the
repo — Electron 42, React 19, Tailwind v4, shadcn/Radix, Vite, vitest, Go 1.25,
`@anthropic-ai/claude-agent-sdk`, gvisor-tap-vsock, HCS, etc. — reach for the **Context7 MCP**
(`resolve-library-id` → `query-docs`) instead of relying on memory or web search. Do this
proactively, even when you think you know the answer; the user shouldn't have to say "use
context7" first.

Skip it for: refactoring, writing scripts from scratch, debugging this repo's own business
logic, code review, and general programming concepts.

## Environment & verification

The dev machine is **macOS (Apple Silicon) or Windows 11**. Each drives its own VM backend:
VZ on macOS, HCS on Windows. Cross-compiling the other target is possible but can't be
fully exercised without the matching host. For Linux build steps (VM image, rootfs):
macOS uses **Docker via OrbStack**; Windows uses **WSL2**.
- TS: verify with typecheck + lint + vitest + `package`; run the Electron window directly.
- Go: verify with `go build ./...` + `go test ./...`; cross-compile `GOOS=windows` to catch
  Windows-only paths. macOS builds need CGO + codesign — use `npm run build:all -- --only=host`.
- End-to-end: `npm run e2e:host` boots a real VM and drives all 11 broker doors + the agent loop
  through the shipped broker (macOS/VZ; `scripts/e2e-host.mjs`) — the deepest integration check,
  complementing the Go unit tests (fake drivers) and `s7-smoke-darwin.sh` (share shape).
- State clearly when something can't be verified (HCS, Windows-only paths, restricted network)
  rather than claiming success.

## Housekeeping

- Don't commit build output or generated code (already gitignored).
- Comments explain WHY, not WHAT; keep them minimal.
- Commit messages: conventional style (`feat`/`fix`/`chore` + scope), focused on the why.
