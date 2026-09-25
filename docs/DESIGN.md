# DIMO Fleet — visual design system

The fleet app shares its visual language with the **DIMO Driver** mobile app
(`dimo-driver/src/theme`): Euclid Circular A, cool blue-black surfaces, the
sky→mint DIMO gradient, and generous radii. Tokens live in
`web/src/global-styles.ts`; this doc is the "how to use them".

## Principles

1. **Mint means live or actionable.** The accent (`--accent`, `--brand-gradient`)
   is reserved for primary actions, the selected/active state, and "online".
   It never decorates. If everything is mint, nothing is.
2. **Ink, not white.** `--primary` is the high-emphasis *text* color (titles,
   key values). It is not a button fill. Primary buttons use the gradient.
3. **Sentence case, one typeface.** Labels are 12px/500 sans in
   `--on-surface-variant`, sentence case, no letter-spacing. No uppercase
   mono "terminal" labels. Identifiers (VIN, token id, plate) use the same face
   with tabular figures (on by default via `font-feature-settings: 'tnum'`).
4. **Surfaces separate by tone, not lines.** Prefer a tonal step
   (`--surface-container-*`) or whitespace over a 1px border. Keep borders for
   inputs, tables' row dividers and the few places a hairline carries meaning.
5. **Radius follows hierarchy.** Chips 5–6px · inputs/buttons-in-rows 10px ·
   cards 16px · floating panels & the app sheet 20px · pills/primary buttons full.

## Tokens (see `global-styles.ts`)

| Role | Token | Dark | Light |
|---|---|---|---|
| App canvas (behind sheet, sidebar) | `--canvas` | `#0E0F11` | `#E7E9E9` |
| Sheet / page background | `--background`, `--surface` | `#16181B` | `#FFFFFF` |
| Card | `--surface-container-low` | `#1C1F22` | `#F6F7F7` |
| Control fill / hover | `--surface-container-high` | `#272A2E` | `#E9EBEB` |
| Hairline | `--outline-variant` | `#2A2E32` | `#E1E4E4` |
| Title ink | `--primary` | `#F6F7F7` | `#131417` |
| Body text | `--on-surface` | `#EDEEEE` | `#131417` |
| Secondary text | `--on-surface-variant` | `#A0A3A2` | `#5E6163` |
| Accent fill | `--accent` | `#46F1E4` | `#22C7BA` |
| Accent as text/icon | `--accent-ink` | `#46F1E4` | `#0B7A72` |
| Text on accent | `--on-accent` | `#06201E` | `#06201E` |
| Accent tints | `--accent-soft`, `--accent-soft-strong` | 12% / 28% | 14% / 30% |
| Primary button | `--brand-gradient` | sky `#8CD0FF` → mint `#46F1E4` | same |
| Status | `--positive` `--warning` `--negative` `--favorite` | | |
| Floating shadow | `--shadow-float` | | |

Map markers can't read CSS variables; use `MAP_COLORS` from
`web/src/utils/fleet-map.ts` (mint = vehicle, sky = selected / route, ink ring).

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
`sharedStyles` where possible. Local primary buttons: `background:
var(--brand-gradient); color: var(--on-accent); border-radius: full;
font: 600 14px/20px; min-height: 40px`. Secondary: `--surface-container-high`
fill + hairline. Destructive: `--error-container` fill, `--error` text.
Button text is sentence case; no uppercase, no letter-spacing.

**Selected / toggled control**: `background: var(--accent-soft-strong);
color: var(--accent-ink)` (not a white slab).

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

**Status dot**: online = `--accent` with `box-shadow: 0 0 8px
var(--accent-soft-strong)`; stale = `--warning`; offline/error = `--error`.

**Floating panels over the map**: `--glass-bg` + `backdrop-filter:
blur(24px) saturate(1.4)`, `--shadow-float`, radius 20px, no border.

## Don'ts

- No `text-transform: uppercase` + tracked mono labels.
- No hex colors in component CSS — add a token instead.
- No `rgba(255,255,255,.5)` hover borders; they vanish in light mode.
- Don't use `--primary` as a button fill.
