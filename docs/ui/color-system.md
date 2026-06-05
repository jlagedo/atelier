# Color System

| Field | Detail |
|---|---|
| Purpose | Define semantic color tokens and usage rules for the desktop UI. |
| Primary reader | Engineers adding or changing renderer components. |
| Source of truth | [`../../apps/desktop/src/renderer/index.css`](../../apps/desktop/src/renderer/index.css). |
| Preview | [`color-mock.html`](color-mock.html). |

Atelier ships two themes derived from real references: a photograph and a
painting. Do not sample colors from other app UIs.

## Source Rules

- Pull palettes from photographs, paintings, film stills, textiles — anything with
  real light in it. Not from other apps.
- Keep the muddiness. Don't "clean up" a sampled hue back to a pure swatch.
- Keep neutrals slightly tinted toward the source (warm paper, cool dusk), never
  flat grey.
- Use glow sparingly — one soft accent, not a neon haze.

## The two themes

| | Light — **"Studio"** | Dark — **"Aegean Dusk"** (hero) |
|---|---|---|
| Source | a watercolour of a painter's atelier | a sunset over water |
| Foundation | warm parchment paper | cool petrol-teal dusk |
| Ink | oxblood-brown (the walls) | warm off-white |
| `--primary` | delft blue (the porcelain) | petrol-teal (the water) |
| `--signal` | honey gold (the wood floor) | sun gold |
| Character | aged paper, antique, calm | dusk, atmospheric |

They aren't two unrelated themes: both resolve to a **cool-blue primary + warm-gold
signal** spine, so they feel like one family seen at two times of day. Only the
*values* differ per theme — the **role mapping is identical**, so components never
branch on theme.

## Token roles

Tokens are defined in [`apps/desktop/src/renderer/index.css`](../../apps/desktop/src/renderer/index.css)
as OKLCH custom properties under `:root` (Studio) and `.dark` (Aegean Dusk), then
exposed to Tailwind utilities via the `@theme inline` block (so `bg-signal`,
`text-positive`, etc. exist). shadcn components consume them by name.

| Role | Token | Meaning / where |
|---|---|---|
| Foundation | `--background` `--card` `--popover` `--border` `--input` | surfaces & lines — ~90% of the UI |
| Ink | `--foreground` `--muted-foreground` | text hierarchy |
| Hover surface | `--accent` | shadcn's subtle hover only |
| **Primary** (cool blue) | `--primary` | the everyday interactive / ops / trust color: file rows, focus rings, suggestion cards, the "Contained" mark, links, default buttons |
| **Signal** (gold) | `--signal` | **rare**, the agent: the send CTA, the assistant's identity/avatar, and "live" states (running / active / starting / resuming) |
| Positive (green) | `--positive` | credit · reconciled · **allowed** · task done · policy-allow |
| Negative (red) | `--destructive` | debit · break · **denied** · blocked-by-policy · destructive actions |
| Warning (sienna/amber) | `--warning` | awaiting attention |
| Tertiary (mauve/oxblood) | `--tertiary` | brand / chart categories |
| Charts | `--chart-1..5` | categorical data viz (primary → signal → positive → negative → tertiary) |

### Atmosphere

`--shadow-lamp` (soft elevation) and `--shadow-signal` (gold glow on the send
button) are **theme-scoped** — warm/paper in Studio, cool/dusk in Aegean Dusk —
via `--shadow-*-raw` vars in each theme block. The `.hero-lamp` mesh behind the
empty-state hero likewise reads per-theme colors from `--hero-1/2/3`.

## Rules

1. **No hardcoded colors in components.** Always use a semantic token utility
   (`bg-primary`, `text-signal`, `text-positive`, …). Never `bg-blue-600`,
   `text-[#…]`, or inline color styles. The one allowed literal is the native
   Electron window `backgroundColor` in `main.ts` (can't read a CSS var; it
   mirrors the dark `--background`).
2. **Green & red are reserved for data/status meaning** — credit/debit,
   reconciled/break, allowed/denied. Never use them as brand decoration. This
   matters in a fund-accounting / transfer-agency UI where green ≈ gain/credit and
   red ≈ loss/break carry real meaning on every screen.
3. **Gold (`--signal`) is rare.** It marks the agent and live activity. If gold is
   everywhere it stops meaning "the AI is here." Trust/ops chrome is blue
   (`--primary`), not gold.
4. **Both themes must work.** Every element references tokens, so toggling
   light/dark must never break contrast or leave a color stuck to one theme.

### Gold vs blue — cheat sheet

| Use **`--signal`** (gold) | Use **`--primary`** (blue) |
|---|---|
| Send button | Suggestion / example cards |
| Assistant avatar & "A" mark | File rows, folder icons |
| Brand mark | Focus rings (`--ring`) |
| Running / active / starting / resuming status | Links, default buttons |
| Running tool-call / task | The "Contained" / trust chrome |
| Hero eyebrow + accent word | Selected-row highlight |

## Adding or changing a color

1. Add the property to **both** `:root` and `.dark` in `index.css` (OKLCH `L C H`).
2. Expose it in the `@theme inline` block (`--color-<name>: var(--<name>);`) so the
   `bg-<name>` / `text-<name>` utilities exist.
3. Use the utility in components — never the raw value.
4. Check contrast: ≥4.5:1 for body text, ≥3:1 for large text and UI boundaries.
