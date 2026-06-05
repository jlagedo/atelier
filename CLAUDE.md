# CLAUDE.md

**Atelier** is a desktop AI workspace that lets an AI agent work on local files safely by
**containment**: a Go host service (`atelierd`) drives a Linux utility VM (VZ on macOS, HCS on
Windows), a Python/OpenHands agent loop runs the AI inside that VM, and an Electron/React app is the
UI. The VM is the cage — safety comes from containment, not per-click consent.

**Per-package commands, conventions, and gotchas** live in `.claude/rules/*.md` and load when you
open files in that package. Deeper architecture reference, when a task needs it:
[`docs/architecture/design.md`](docs/architecture/design.md) (read the relevant section, not the
whole file); current implementation state: [`docs/status/implementation-status.md`](docs/status/implementation-status.md); run guide: [`README.md`](README.md).

## Repo layout

| Dir | What | State |
| --- | --- | --- |
| `apps/desktop` | Electron/React desktop UI | WORK mode wired to the broker; chat mode mock |
| `services` | One Go module — host broker (`atelierd`), in-VM daemon (`runner`), dev CLI (`atelierctl`) | full substrate (boot/exec/files/net) |
| `packages/partisan` | Python/OpenHands in-guest agent loop (`cli_guest.py`) — the sole agent | live; LiteLLM picks the provider |
| `packages/protocol` | Generated Hop-2 protocol bindings (schema is canonical) | generated, gitignored |
| `image` | VM image build — kernel + initrd + rootfs bundle + runner volume | build pipeline |
| `tools/protogen` | Protocol codegen (schema → TS + Go) | working |
| `docs` | Design, runtime architecture, implementation, and security docs | see `docs/README.md` |

Generated/build output is gitignored: `build/`, `apps/desktop/.vite`, `apps/desktop/out`,
`**/node_modules`, `packages/protocol/src`, `services/pkg/protocol`, `services/bin`, `image/.work`,
`image/bundle`.

### Where things live (jump here, don't search)

| To touch… | Go to |
| --- | --- |
| Policy gate / containment chokepoint | `services/internal/broker/broker.go` |
| Files door (workspace path jailing) | `services/internal/broker/files.go` |
| macOS VZ driver | `services/internal/vmm/driver_darwin.go` |
| VM lifecycle | `services/internal/vmm/manager.go` |
| Egress jail (default-deny network) | `services/internal/netjail/network.go` |
| Windows HCS bindings | `services/internal/hcs/computecore_windows.go` |
| Session Manager (host state machine) | `apps/desktop/src/main/sessions/manager.ts` |
| Hop-2 named-pipe JSON-RPC client | `apps/desktop/src/main/host-client/client.ts` |
| In-guest agent loop (the sole agent) | `packages/partisan/cli_guest.py` |
| In-guest agent wire client (NDJSON codec + transport seam) | `apps/desktop/src/main/sessions/client.ts` (`PartisanClient`), `transport.ts` |
| Protocol (canonical schema) | `packages/protocol/schema/protocol.json` |

## Build & verify the whole stack

`scripts/build-all.mjs` is the single build entrypoint — it builds + verifies everything into one
tree, `build/<config>/` (all gitignored). The heavy rootfs/kernel/initrd image is skipped by default;
the cheaper `runner` volume (runner + in-guest agent, via `uv sync`) rebuilds in its place. Pass
`--image` to rebuild the full bundle. Both need Docker (OrbStack on macOS, WSL2 on Windows).

```sh
npm run build:all                      # debug: host + desktop + runner volume; image skipped
npm run build:all -- --image           # also rebuild the heavy VM image (rootfs+kernel+initrd)
npm run build:all -- --config=release  # stripped Go + self-contained -> build/release/
npm run build:all -- --only=host       # one phase only (host | image | desktop)
npm run build:all -- --deep            # true from-zero: also wipe node_modules + image/.work
npm run build:all -- --no-verify       # skip tests/typecheck/lint
```

Run the broker (`build/<config>/atelierd`; elevated only on Windows) and the app
(`ATELIER_BUNDLE_DIR=build/<config>/image/<target> npm run dev`). Building the broker on macOS
requires codesigning (VZ refuses an unsigned broker); use the orchestrator, not a bare `go build`.
Image-build internals: `image/build.sh`. Full run guide + `atelierctl` terminal path + dev-without-VM:
[`README.md`](README.md).

### The verification gate

Any change touching the host broker (`services`), the in-guest daemon/agent (`runner`,
`packages/partisan`), or the VM image (`image/`) is **not done** until `npm run build:all` then
`npm run e2e:host` pass — and when you add behavior, add a matching assertion to
`scripts/e2e-host.mjs`. The per-package fast-loop checks (in `.claude/rules/*.md`) are not a
substitute. `e2e:host` needs `ANTHROPIC_API_KEY` and a real VZ boot on macOS; if you can't run it,
say so rather than claiming success. State clearly when something can't be verified (HCS,
Windows-only paths, restricted network).

## Library docs — use Context7

This stack runs latest-stable libraries (Electron 42, React 19, Tailwind v4, Go 1.25,
`openhands-sdk`/`openhands-tools`, LiteLLM, gvisor-tap-vsock, etc.), so training data is often stale.
For current API syntax, config, setup, version migration, or library-specific debugging, reach for
the **Context7 MCP** (`resolve-library-id` → `query-docs`) proactively, without being asked first.
Skip it for refactoring, scripts from scratch, this repo's own business logic, code review, and
general programming concepts.

## Housekeeping

- Don't commit build output or generated code (already gitignored).
- After editing any Markdown, run `npm run lint:md` (config in `.markdownlint-cli2.jsonc`); it must
  pass clean.
- Comments explain WHY, not WHAT; keep them minimal.
- Commit messages: conventional style (`feat`/`fix`/`chore` + scope), focused on the why.
