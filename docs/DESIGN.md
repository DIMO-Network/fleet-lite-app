# DIMO Fleet — visual design system

The fleet app shares its visual language with the **DIMO Driver** mobile app
(`dimo-driver/src/theme`): Euclid Circular A, cool blue-black surfaces, the
sky→mint DIMO gradient, and generous radii. Tokens live in
`web/src/global-styles.ts`; this doc is the "how to use them".

## Principles

1. **Ink acts, teal means live.** Primary actions are solid ink
   (`--btn-primary-*`, the inverse of the surface). The accent (`--accent`) is
   a *status* colour only: online dots, a charge in progress, the "Active"
   badge, success banners. It is never an action and never a selection. If
   everything is teal, nothing is live.
2. **The gradient is a brand moment.** `--brand-gradient` (sky → mint) is for
   sign-in, onboarding submit and progress fills (`--progress-fill`, which
   deepens in light mode so it stays visible). Not for everyday buttons.
3. **Selection is inverse ink.** A toggled control (filters, durations, map
   tools, member toggles) uses `--selected-bg` / `--selected-fg`. A selected
   row or card gets a neutral fill and an ink edge (`inset 3px 0 0
   var(--primary)`), not a tint.
4. **Sentence case, one typeface.** Labels are 12px/500 sans in
   `--on-surface-variant`, sentence case, no letter-spacing. No uppercase
   mono "terminal" labels. Identifiers (VIN, token id, plate) use the same face
   with tabular figures (on by default via `font-feature-settings: 'tnum'`).
5. **Surfaces separate by tone, not lines.** Prefer a tonal step
   (`--surface-container-*`) or whitespace over a 1px border. Keep borders for
   form controls, tables' row dividers and the few places a hairline carries
   meaning.
6. **Radius follows hierarchy.** Chips 5–6px · inputs/buttons-in-rows 10px ·
   cards 16px · floating panels & the app sheet 20px · pills/primary buttons full.
7. **Contrast is measured, in both themes.** Text ≥ 4.5:1 on the surface it
   sits on (badges: on their own tint); control edges, focus rings, status
   dots and progress fills ≥ 3:1. Check a new colour against every surface it
   lands on before adding it.

## Tokens (see `global-styles.ts`)

| Role | Token | Dark | Light |
|---|---|---|---|
| App canvas (behind sheet, sidebar) | `--canvas` | `#0E0F11` | `#E7E9E9` |
| Sheet / page background | `--background`, `--surface` | `#16181B` | `#FFFFFF` |
| Card | `--surface-container-low` | `#1C1F22` | `#F6F7F7` |
| Control fill / hover | `--surface-container-high` | `#272A2E` | `#E9EBEB` |
| Hairline (dividers, table rows) | `--outline-variant` | `#373B40` | `#D2D6D7` |
| Line drawn on the canvas (sidebar, onboarding) | `--canvas-divider` | `#45494E` | `#A0A3A2` |
| Form control edge (input, select, textarea) | `--control-border` / `-hover` | `#747A7F` / `#A0A3A2` | `#7C8082` / `#5E6163` |
| Select chevron | `--select-chevron` | per theme (data URI) | |
| Keyboard focus outline | `--focus-ring` | `#46F1E4` | `#0B7A72` |
| Title ink | `--primary` | `#F6F7F7` | `#131417` |
| Body text | `--on-surface` | `#EDEEEE` | `#131417` |
| Secondary text | `--on-surface-variant` | `#A0A3A2` | `#5E6163` |
| Primary action | `--btn-primary-bg` / `-fg` / `-hover` | `#F6F7F7` / `#111214` / `#FFFFFF` | `#131417` / `#FFFFFF` / `#2E3236` |
| Toggled / selected control | `--selected-bg` / `--selected-fg` | inverse surface / inverse text | same |
| Live status (dots) | `--accent` | `#46F1E4` | `#0B8F85` |
| Accent as text/icon | `--accent-ink` | `#46F1E4` | `#07635C` |
| Accent tints | `--accent-soft`, `--accent-soft-strong` | 12% / 28% | 14% / 30% |
| Brand gradient | `--brand-gradient` | sky `#8CD0FF` → mint `#46F1E4` | same |
| Progress fill | `--progress-fill` | brand gradient | `#1E6FBF` → `#0A7069` |
| Status | `--positive` `--warning` `--negative` | `#36DF71` `#FFAC60` `#FF6060` | `#11672F` `#8F4500` `#C70000` |
| Modal backdrop | `--scrim` | 62% near-black | 32% ink |
| Floating shadow | `--shadow-float` | | |

Map markers can't read CSS variables; use `MAP_COLORS` and `tripMapStyles`
from `web/src/utils/fleet-map.ts`: mint = vehicle; selected vehicle = sky with
an ink ring; trip route = sky on dark tiles, a deeper blue on light; trip start
= solid green dot, end = red ring on a white core (they differ in shape as well
as hue).

## Type scale

| Use | Spec |
|---|---|
| Page title | `var(--type-headline-md)` (20/28 600), `letter-spacing: -0.01em`, `--primary` |
| Section / card title | `600 15px/22px var(--font-headline)` or `600 17px/24px` for panel headers |
| Big metric | `var(--type-data-display)` (40/44 600), `letter-spacing: -0.03em` |
| Body | `var(--type-body-md)` 15/22 · dense `var(--type-body-sm)` 14/20 |
| Label / table header / meta | `var(--type-label)` 12/16 500, `--on-surface-variant` |

## Patterns

**Page header** (`header.top-bar`): 72px tall, `padding: 0 var(--gutter)`,
no bottom border, transparent background (the map view fades it into the map
with a gradient instead). Title + optional view switch on the left; actions and
`<tenant-switcher>` on the right.

**View switch / tabs**: segmented pill — container `--surface-container-high`,
`padding: 3px`, `radius: full`; items `500 13px/18px`, `padding: 6px 14px`;
active item `background: var(--surface-bright)`, `color: var(--primary)`,
`box-shadow: 0 1px 2px rgba(0,0,0,.2)`. No underlines.

**Buttons**: use `.btn-primary` / `.btn-secondary` / `.btn-ghost` from
`sharedStyles`. A component that needs its own primary button uses the same
tokens: `background: var(--btn-primary-bg); color: var(--btn-primary-fg);
border-radius: full; font: 600 14px/20px; min-height: 40px`, hover
`--btn-primary-hover`. Secondary: `--surface-container-high` fill + hairline.
Destructive: `--error-container` fill, `--error` text. Button text is sentence
case; no uppercase, no letter-spacing.

**Selected / toggled control**: `background: var(--selected-bg); color:
var(--selected-fg)`. Selected rows/cards: `--surface-container-high` fill,
`box-shadow: inset 3px 0 0 var(--primary)`.

**Form controls**: `--surface-container-high` fill, `1px solid
var(--control-border)`, hover `--control-border-hover`, focus
`border-color: var(--focus-ring)` with a `0 0 0 3px var(--accent-soft)` halo
(one ring, on the control, not on a wrapper too).

**Cards**: `--surface-container-low`, `radius: var(--radius-lg)`, border only
if the card sits on an equal-tone surface. Hover = one tonal step up, not a
brighter border.

**Tables**: header row `var(--type-label)` in `--on-surface-variant`,
sentence case, no background fill; rows 52–56px, divider
`1px solid var(--outline-variant)`, hover `--surface-container-low`; numbers
right-aligned (tabular figures are already on).

**Chips**: plate = `600 11px/16px`, `letter-spacing: .06em`,
`--surface-container-highest`, radius 5px. Group chip = 12px/500 text on a
14% tint of the group color with a 6px dot.

**Status dot**: online / charging now = `--accent` with `box-shadow: 0 0 8px
var(--accent-soft-strong)`; stale = `--warning`; offline/error = `--error`.
A warning reading (e.g. coolant out of range) is `--warning`, never the
all-clear `--positive`.

**Floating panels over the map**: `--glass-bg` + `backdrop-filter:
blur(24px) saturate(1.4)`, `--shadow-float`, radius 20px, no border.

## Don'ts

- No `text-transform: uppercase` + tracked mono labels.
- No hex colors in component CSS — add a token instead (map markers excepted,
  see above).
- No `rgba(255,255,255,.5)` hover borders; they vanish in light mode.
- No teal for actions, hovers or selection — it is the live-status colour.
- No `--brand-gradient` on everyday buttons.
- Don't draw form controls with `--outline-variant`; it is a hairline and
  fails 3:1 as a control edge.
- Don't hide a control until hover — touch screens have no hover.
