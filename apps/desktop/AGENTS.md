# AGENTS.md — `apps/desktop`

The **Atelier** desktop app: the chat-forward Electron/React shell that drives a contained AI agent.
This file covers the Electron Forge app only — its build, process model, IPC contract, and
conventions. For the wider stack (Go broker, in-guest agent loop, VM image, protocol codegen) and
the design rationale, see the repo-root [`../../AGENTS.md`](../../AGENTS.md) and
[`../../docs/design.md`](../../docs/design.md).

## Commands

```sh
npm install
npm start            # dev (Forge + Vite, HMR); headless box: xvfb-run -a npm start
npm run typecheck    # tsc --noEmit
npm run lint         # oxlint
npm run format       # oxfmt (formats *.ts/*.tsx)
npm test             # vitest run
npm run package      # full Forge build, no window (good for CI / headless verify)
npm run make         # package + maker-zip (cross-platform smoke artifact)
```

Node ≥ 22.12. The model call needs `ANTHROPIC_API_KEY` in the environment that launches the app;
WORK mode also needs the Go broker running (see root README). Without the broker the app still boots
— WORK mode shows the "host service isn't running" placeholder.

## Toolchain

Electron Forge 7 + `@electron-forge/plugin-vite`, Vite 8 (Rolldown), React 19, TypeScript (strict),
Tailwind v4, shadcn/ui (new-york; Radix + cva + tailwind-merge), `react-markdown`/`remark-gfm`,
Phosphor icons, IBM Plex fonts, oxlint/oxfmt, vitest + Testing Library (jsdom). These are
**latest-stable**, ahead of design §11's Cowork pins — when you need current API details for any of
them, reach for the **Context7 MCP** rather than memory (see root AGENTS.md "Library docs").

## Build wiring

Forge drives three Vite builds (`forge.config.ts`), one per Electron process target:

| Target | Entry | Config | Notes |
|---|---|---|---|
| `main` | `src/main/main.ts` | `vite.main.config.ts` | Node; `electron` is `external`, never bundled |
| `preload` | `src/preload/preload.ts` | `vite.preload.config.ts` | Node bridge; `electron` external |
| renderer (`main_window`) | `index.html` → `src/renderer/main.tsx` | `vite.renderer.config.ts` | browser; React + Tailwind plugins |

- Forge injects the `MAIN_WINDOW_VITE_DEV_SERVER_URL` / `MAIN_WINDOW_VITE_NAME` globals
  (`forge.env.d.ts`) — main loads the dev server in dev, the built `index.html` when packaged.
- `vite.shared.ts` (`quietSingleFileBundle`) is a workaround for plugin-vite still emitting the
  deprecated `inlineDynamicImports` under Vite 8 — remove when plugin-vite targets Vite 8 options.
- `@` aliases `src/renderer` in both `vite.renderer.config.ts` and `vitest.config.ts` (and
  `tsconfig.json` paths). Renderer code imports UI as `@/...`; it may import **types** from
  `../main/ipc/*` but never main runtime code.
- Tailwind v4 has **no** `postcss.config`/`tailwind.config` — it's wired via `@tailwindcss/vite`
  plus `@import "tailwindcss"` / `@plugin` / `@custom-variant` in `src/renderer/index.css`.
- shadcn config is `components.json` (style new-york, icon library phosphor, css variables).

## Process model & layout

```
src/main/      Node main process
  main.ts          window + lifecycle; installs CSP, registers IPC, tears down on will-quit
  security.ts      CSP (dev-relaxed for HMR/eval, prod-strict)
  ipc/             channels.ts (names) · handlers.ts (ipcMain) · types.ts (payloads)
  host-client/     Hop-2 named-pipe JSON-RPC client to the Go broker (see its README)
  sessions/        manager.ts (host-owned WORK state machine) + store.ts (durable)
  workspace/       reader.ts + watcher.ts — mirror the session folder back to the UI
src/preload/
  preload.ts       the ONLY renderer↔main bridge: contextBridge `window.atelier`
src/renderer/    sandboxed React (no Node)
  features/{chat,sessions,workspace}/   chat view+composer, session list/mode/status, file panel
  components/ui/   shadcn primitives        components/   app-sidebar, theme-*
  hooks/           use-work-sessions.ts (live WORK store), use-mobile.ts
  lib/             utils.ts (cn) · mock-data.ts (chat-mode mock + shared renderer types)
```

The **Session Manager** (`sessions/manager.ts`) is the heart of WORK mode: brings up the shared VM
once, then per session mounts the folder, launches a persistent in-guest loop (`cli-guest --serve`),
feeds turns, streams NDJSON events to the renderer, and hibernates idle/LRU sessions to bound guest
memory. Detailed design lives in that file's header and root AGENTS.md.

## Security model (don't weaken without reason — design §2)

- Window `webPreferences`: `sandbox: true`, `contextIsolation: true`, `nodeIntegration: false`.
- The renderer's **only** capability surface is the narrow allowlisted `contextBridge` in
  `preload.ts` (`window.atelier`). Add a capability there deliberately, never widen it broadly.
- CSP is set in `security.ts`: prod is strict (`script-src 'self'`, no eval); dev relaxes for Vite
  HMR + React Refresh (`'unsafe-inline' 'unsafe-eval'`, localhost ws/http). Keep the prod list tight.

## IPC contract (Hop 1)

`src/main/ipc/channels.ts` is the single source of truth for channel names, imported by both
`handlers.ts` (main) and `preload.ts` (renderer). To add an IPC call:

1. Add the name to `IpcChannel` in `channels.ts`.
2. Add the payload type to `ipc/types.ts`.
3. `ipcMain.handle` it in `handlers.ts` (invoke = request/response) **or** broadcast it via the
   `ManagerEmitter` (push = main→renderer `webContents.send`, each carries an `appId`/`sessionId`).
4. Expose it on the `api` object in `preload.ts` (typed; `invoke` for requests, `subscribe` for pushes).

Renderer code talks to the backend only through `window.atelier` — never `ipcRenderer` directly.

## Conventions

- **Don't fight the framework.** Use Electron/Vite/React/Tailwind the way they're meant to be used;
  reach for the platform's own mechanism before a custom workaround. If you find yourself patching
  around a framework, stop and check the docs (Context7) first.
- **Follow shadcn/ui conventions.** Add primitives the shadcn way (the registry/CLI, `components.json`
  aliases) into `components/ui`; compose with `cva` + `cn` (`tailwind-merge`) and theme via the CSS
  variables in `index.css` — don't hand-roll bespoke components or hardcode colors when a shadcn
  primitive or token exists.
- **Use the design system.** When working on UI, style from the design tokens (the CSS variables in
  `index.css`) and shadcn primitives. When a change spans multiple components or is structural (a new
  surface, spacing/radius scale, semantic color), add or update a token in the design system rather
  than repeating one-off values — keep ad-hoc styles truly local.
- **Use Context7 for docs.** Fetch current framework/library documentation via the Context7 MCP
  (`resolve-library-id` → `query-docs`) — Electron 42, React 19, Tailwind v4, shadcn/Radix, Vite 8,
  vitest, etc. This stack runs latest-stable, so training data is often stale; do it proactively.
- WORK mode drives the real broker; **chat mode is still mock** (`renderer/lib/mock-data.ts`).
- TS is strict + `verbatimModuleSyntax` — import types with `import type`.
- Comments explain WHY, not WHAT; keep them minimal. Format with oxfmt, lint with oxlint before done.
- Env knobs (read in main / Session Manager): `ATELIER_BUNDLE_DIR` (default `image\bundle`),
  `ATELIER_IDLE_MS` (hibernate-after-idle, default 10 min), `ATELIER_MAX_ACTIVE` (live loops before
  LRU hibernate, default 3), `ATELIER_BOOT_TIMEOUT_MS` (default 120 000).

## Testing & verification

- Tests are `src/**/*.test.{ts,tsx}` (vitest, jsdom; setup `src/test/setup.ts`). Current coverage:
  `host-client/client.test.ts` (codec/framing) and `renderer/App.test.tsx`.
- The dev box here is **headless Linux**; the app's real target is **Windows + HCS**. Verify with
  `typecheck` + `lint` + `test` + `package`; use `xvfb-run -a npm start` to boot a real window.
- Full WORK mode (VM boot/exec/files) can't run here — it needs Windows 11 + HCS. Say so plainly
  rather than claiming a path was verified end-to-end.
