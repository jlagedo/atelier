# apps/desktop

Electron app — the chat-forward UI shell. [M6]

**Feature 0 scaffold:** a runnable, hardened Electron shell with a static mock chat layout.
No real agent, Go host, or backend yet — those arrive in later milestones.

## Stack

Electron Forge (`@electron-forge/plugin-vite`) · Vite · React 19 · TypeScript · Tailwind v4
· oxlint/oxfmt · vitest.

> **Note — diverges from `docs/design.md` §11.** The design pins Cowork's versions
> (Electron 41 / React 18 / Vite 6 / Tailwind **3.4**). This scaffold uses **latest stable**
> instead (Electron 42 / React 19 / Vite 8 / Tailwind **4**). Tailwind v4 changes setup:
> no `postcss.config`/`tailwind.config`; it's wired via `@tailwindcss/vite` plus
> `@import "tailwindcss"` + `@plugin` directives in `src/renderer/index.css`.

## Layout

| Path | Role |
|---|---|
| `src/main/` | Electron main process (hardened window, app lifecycle) |
| `src/main/security.ts` | strict Content-Security-Policy (dev-relaxed, prod-strict) |
| `src/main/ipc/` | typed `ipcMain` handlers + shared channel names (Hop 1) |
| `src/preload/` | narrow allowlisted `contextBridge` (`window.atelier`) |
| `src/renderer/` | React UI; `features/ChatView.tsx` is the mock chat layout |
| `forge.config.ts` · `vite.*.config.ts` | Forge + Vite wiring |

Hardening (design §2): `sandbox: true`, `contextIsolation: true`, `nodeIntegration: false`,
strict CSP, and a minimal preload bridge. The one wired IPC round-trip is `app:getVersion`.

## Scripts

```sh
npm install          # from this directory
npm start            # dev (needs a display; on a headless box: xvfb-run -a npm start)
npm run package      # build the full app (no window)
npm run typecheck    # tsc --noEmit
npm run lint         # oxlint
npm run format       # oxfmt
npm test             # vitest
```
