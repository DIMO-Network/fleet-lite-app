# Light Mode Design

**Date:** 2026-06-18
**Status:** Approved

## Overview

Add a manual light/dark theme toggle to fleet-lite-app. The app is currently dark-only. The toggle lives in the global top bar, persists to `localStorage`, and switches all surfaces — including Leaflet map tiles — in a single attribute flip on `<html>`.

## Architecture

### Token migration

All CSS custom property values currently defined on `:host` inside `sharedStyles` (in `web/src/global-styles.ts`) move to the document-level stylesheet as two blocks:

- `:root` — dark mode (current defaults, unchanged values)
- `:root[data-theme="light"]` — light overrides for surface, primary, outline, and glass tokens

CSS custom properties inherit through shadow DOM boundaries, so every Lit component picks up the new values automatically with zero per-component changes.

The `sharedStyles` `:host` block retains only layout/spacing/radius/typography tokens and color-role references (`color: var(--on-surface)` etc.) — no raw hex values remain there.

Two hardcoded values in `sharedStyles` are converted to tokens:
- `.glass-panel` background: `rgba(28, 27, 27, 0.85)` → `var(--glass-bg)`
- `documentStyles` body `background` and `color` → `var(--surface)` / `var(--on-surface)`

### ThemeService

New file: `web/src/services/theme-service.ts`

```
class ThemeService {
  get current(): 'dark' | 'light'
  init(): void          // call once from app-root connectedCallback
  toggle(): void        // flip theme, persist, dispatch 'theme-change' on window
}
export const themeService = new ThemeService();
```

`init()` reads `localStorage.getItem('theme')` (defaults to `'dark'`) and sets `document.documentElement.dataset.theme` accordingly (omits the attribute for dark so `:root` defaults apply, sets `data-theme="light"` for light).

`toggle()` flips `current`, writes to localStorage, updates the attribute, and dispatches `new CustomEvent('theme-change', { detail: { theme } })` on `window`.

### Toggle button

Added to `app-root.ts` top bar, right side, as an `.icon-btn`. Uses Material Symbol icons:
- Current theme dark → show `light_mode` icon (click → go light)
- Current theme light → show `dark_mode` icon (click → go dark)

`app-root` holds `@state() private theme: 'dark' | 'light' = 'dark'` and updates it on toggle so the icon re-renders.

### Map tile switching

Three components hard-reference `dark_all` CARTO tiles:
- `web/src/views/fleet-overview.ts`
- `web/src/elements/vehicle-trips-panel.ts`
- `web/src/elements/trip-replay-modal.ts`

Each component:
1. Extracts tile initialization into a `buildTileLayer()` method that reads `themeService.current` to select the URL
2. Adds a `handleThemeChange` arrow function that removes the current tile layer, calls `buildTileLayer()`, and adds the new layer to the map
3. Registers `window.addEventListener('theme-change', this.handleThemeChange)` in `connectedCallback` (or after map init for `fleet-overview`)
4. Removes the listener in `disconnectedCallback`

Tile URLs:
- Dark: `https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png`
- Light: `https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png`

The `brightness(1.8)` filter on `.leaflet-tile` (currently hardcoded in `fleet-overview.ts`) becomes conditional: applied only in dark mode via a `.dark-tiles` class on `.map` that is toggled alongside the tile swap.

## Light Mode Color Palette

| Token | Dark | Light |
|---|---|---|
| `--surface` / `--background` | `#131313` | `#f8f8f8` |
| `--surface-dim` | `#131313` | `#efefef` |
| `--surface-bright` | `#393939` | `#ffffff` |
| `--surface-container-lowest` | `#0e0e0e` | `#ffffff` |
| `--surface-container-low` | `#1c1b1b` | `#f2f2f2` |
| `--surface-container` | `#201f1f` | `#ebebeb` |
| `--surface-container-high` | `#2a2a2a` | `#e2e2e2` |
| `--surface-container-highest` | `#353534` | `#d9d9d9` |
| `--surface-variant` | `#353534` | `#e0e0e0` |
| `--on-surface` / `--on-background` | `#e5e2e1` | `#1a1a1a` |
| `--on-surface-variant` | `#c4c7c8` | `#444748` |
| `--inverse-surface` | `#e5e2e1` | `#2f3131` |
| `--inverse-on-surface` | `#313030` | `#f0f0f0` |
| `--outline` | `#8e9192` | `#6e7172` |
| `--outline-variant` | `#444748` | `#c4c7c8` |
| `--primary` | `#ffffff` | `#1a1a1a` |
| `--on-primary` | `#2f3131` | `#ffffff` |
| `--primary-container` | `#e2e2e2` | `#2f3131` |
| `--on-primary-container` | `#636565` | `#e0e0e0` |
| `--inverse-primary` | `#5d5f5f` | `#c6c6c7` |
| `--glass-bg` | `rgba(28,27,27,0.85)` | `rgba(248,248,248,0.85)` |

Secondary (kinetic orange), tertiary (status green), and error tokens are identical in both modes.

## Files Changed

| File | Change |
|---|---|
| `web/src/global-styles.ts` | Move token values to `:root` / `:root[data-theme="light"]`; add `--glass-bg` token; fix hardcoded body styles |
| `web/src/services/theme-service.ts` | New file — ThemeService singleton |
| `web/src/views/app-root.ts` | Add toggle button; call `themeService.init()`; track `@state() theme` |
| `web/src/views/fleet-overview.ts` | Tile layer switching; conditional brightness filter |
| `web/src/elements/vehicle-trips-panel.ts` | Tile layer switching |
| `web/src/elements/trip-replay-modal.ts` | Tile layer switching |

## Out of Scope

- System preference (`prefers-color-scheme`) auto-detection — manual toggle only
- Per-view theme overrides
- Animated theme transition (crossfade)
