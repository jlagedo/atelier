---
paths:
  - "apps/desktop/**"
---

# Desktop app — `apps/desktop` (TypeScript / Electron)

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
`electron-forge package` silently exit 0 with no `out/`. Still required on every upgrade path (even
`@electron/packager@20` pins `extract-zip@2`).

## Process layout

- `src/main` — Node main process. `host-client/` is the Hop-2 named-pipe JSON-RPC client to the Go
  broker; `sessions/` is the **Session Manager** (`manager.ts`) + durable `store.ts` — the
  host-owned state machine that brings up `vm0` once and runs **concurrent persistent per-session
  in-guest loops** (`cli_guest.py --serve`), with idle/LRU **hibernate→resume** to bound guest
  memory. The wire to each loop is `PartisanClient` (`sessions/client.ts`) — NDJSON codec +
  `export_context` correlation over a `LoopTransport` seam (`transport.ts`) that runs the **same**
  client over the broker `exec` door (prod) or a spawned subprocess (tests), so the wire client is
  tested against the real agent without a VM. `workspace/` reads + watches the session folder to
  mirror deliverables back to the UI.
- `src/renderer` — sandboxed React. `features/{chat,sessions,workspace}` (chat view + composer,
  session list/mode/status, file panel), `components/ui` (shadcn primitives).

## Conventions

- Renderer is hardened: `sandbox: true`, `contextIsolation: true`,
  `nodeIntegration: false`, strict CSP (`src/main/security.ts` — dev-relaxed for HMR, prod-strict).
- The renderer's only bridge is a narrow `contextBridge` (`window.atelier`) in `src/preload`.
- IPC channel names are centralized in `src/main/ipc/channels.ts` (shared by main + preload).
- Tailwind v4: no `postcss.config`/`tailwind.config`; wired via `@tailwindcss/vite` +
  `@import "tailwindcss"` / `@plugin` in `src/renderer/index.css`.
- WORK mode drives the real broker; chat mode is still mock (`renderer/lib/mock-data.ts`).
- Env knobs: `ATELIER_BUNDLE_DIR` (per-target bundle dir), `ATELIER_IDLE_MS` (hibernate-after-idle,
  default 10 min), `ATELIER_MAX_ACTIVE` (live loops before LRU hibernate, default 3),
  `ATELIER_BOOT_TIMEOUT_MS` (default 120 000). The model call needs `ANTHROPIC_API_KEY` in the
  launching environment.
