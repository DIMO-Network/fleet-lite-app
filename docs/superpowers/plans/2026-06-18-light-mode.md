# Light Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a manually-toggled light/dark theme to fleet-lite-app, persisted to localStorage, with CARTO tile swapping on all three map components.

**Architecture:** CSS custom properties move from `sharedStyles :host` to document-level `:root` / `:root[data-theme="light"]` blocks so they cascade through all shadow roots automatically. A `ThemeService` singleton flips `document.documentElement.dataset.theme` and dispatches a `theme-change` window event that the three map components use to swap tile layers. The toggle button lives in the `side-nav` footer (the app's only persistent global chrome).

**Tech Stack:** Lit 3, TypeScript, CSS custom properties, Leaflet, localStorage

## Global Constraints

- Dark mode is default — no `data-theme` attribute on `<html>` means dark
- localStorage key: `'fleet-theme'`; value: `'light'` (only stored when light)
- `theme-change` CustomEvent detail shape: `{ theme: 'dark' | 'light' }`
- CARTO dark tile URL: `https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png`
- CARTO light tile URL: `https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png`
- Tile attribution (both): `'&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>'`
- Tile options (both): `{ subdomains: 'abcd', maxZoom: 19 }`
- `brightness(1.8)` filter on `.leaflet-tile` applies **only** in dark mode via a `.dark-tiles` class on the map container

---

### Task 1: CSS Token Migration

**Files:**
- Modify: `web/src/global-styles.ts`

**Interfaces:**
- Produces: `--glass-bg` CSS token available in both modes; all color tokens available on `:root` / `:root[data-theme="light"]` so every shadow root inherits them

- [ ] **Step 1: Replace the full content of `web/src/global-styles.ts`**

```typescript
import { css } from 'lit';

export const sharedStyles = css`
    :host {
        /* ---------------- Typography ---------------- */
        --font-headline: 'Inter', sans-serif;
        --font-body: 'Inter', sans-serif;
        --font-mono: 'JetBrains Mono', monospace;

        --type-headline-xl: 700 40px/48px var(--font-headline);
        --type-headline-lg: 600 32px/40px var(--font-headline);
        --type-headline-md: 600 24px/32px var(--font-headline);
        --type-body-lg: 400 18px/28px var(--font-body);
        --type-body-md: 400 16px/24px var(--font-body);
        --type-body-sm: 400 14px/20px var(--font-body);
        --type-label-caps: 600 12px/16px var(--font-mono);
        --type-data-display: 500 48px/56px var(--font-headline);

        /* ---------------- Spacing ---------------- */
        --sidebar-width: 280px;
        --container-max-width: 1440px;
        --gutter: 24px;
        --margin-desktop: 40px;
        --margin-mobile: 16px;
        --stack-sm: 8px;
        --stack-md: 16px;
        --stack-lg: 32px;

        /* ---------------- Radii ---------------- */
        --radius-sm: 0.25rem;
        --radius-md: 0.5rem;
        --radius-lg: 0.75rem;
        --radius-xl: 1rem;
        --radius-2xl: 1.5rem;
        --radius-full: 9999px;

        color: var(--on-surface);
        font: var(--type-body-md);
    }

    *,
    *::before,
    *::after {
        box-sizing: border-box;
        margin: 0;
        padding: 0;
    }

    *:focus-visible {
        outline: 1px solid var(--primary);
        outline-offset: 2px;
    }

    /* Material Symbols must be redeclared inside each Shadow DOM */
    .material-symbols-outlined {
        font-family: 'Material Symbols Outlined';
        font-weight: normal;
        font-style: normal;
        font-size: 24px;
        line-height: 1;
        letter-spacing: normal;
        text-transform: none;
        display: inline-block;
        white-space: nowrap;
        word-wrap: normal;
        direction: ltr;
        -webkit-font-smoothing: antialiased;
        -moz-osx-font-smoothing: grayscale;
        text-rendering: optimizeLegibility;
        font-feature-settings: 'liga';
    }

    .material-symbols-outlined.filled {
        font-variation-settings: 'FILL' 1;
    }

    /* ---------------- Type utility classes ---------------- */
    .t-headline-xl { font: var(--type-headline-xl); letter-spacing: -0.02em; }
    .t-headline-lg { font: var(--type-headline-lg); letter-spacing: -0.01em; }
    .t-headline-md { font: var(--type-headline-md); }
    .t-body-lg     { font: var(--type-body-lg); }
    .t-body-md     { font: var(--type-body-md); }
    .t-body-sm     { font: var(--type-body-sm); }
    .t-label-caps  { font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; }
    .t-data        { font: var(--type-data-display); letter-spacing: -0.03em; }

    /* ---------------- Buttons ---------------- */
    button {
        font: inherit;
        color: inherit;
        background: none;
        border: none;
        cursor: pointer;
    }

    .btn-primary {
        background: var(--primary);
        color: var(--on-primary);
        padding: 12px 16px;
        border-radius: var(--radius-md);
        font: var(--type-label-caps);
        letter-spacing: 0.05em;
        text-transform: uppercase;
        transition: opacity 0.15s ease;
    }
    .btn-primary:hover { opacity: 0.9; }

    .btn-secondary {
        background: transparent;
        color: var(--primary);
        border: 1px solid var(--primary);
        padding: 12px 16px;
        border-radius: var(--radius-md);
        font: var(--type-label-caps);
        letter-spacing: 0.05em;
        text-transform: uppercase;
    }

    .btn-ghost {
        color: var(--on-surface-variant);
        padding: 8px;
        border-radius: var(--radius-full);
        transition: background 0.15s ease;
    }
    .btn-ghost:hover { background: var(--surface-container-high); color: var(--primary); }

    /* ---------------- Card ---------------- */
    .card {
        background: var(--surface-container-low);
        border: 1px solid var(--outline-variant);
        border-radius: var(--radius-lg);
        padding: var(--gutter);
    }

    /* ---------------- Glass panel (used by the map overlay list) ---------------- */
    .glass-panel {
        background: var(--glass-bg);
        backdrop-filter: blur(12px);
        -webkit-backdrop-filter: blur(12px);
    }

    /* ---------------- Scrollbar ---------------- */
    .custom-scrollbar::-webkit-scrollbar { width: 6px; }
    .custom-scrollbar::-webkit-scrollbar-track { background: transparent; }
    .custom-scrollbar::-webkit-scrollbar-thumb {
        background-color: var(--outline-variant);
        border-radius: 10px;
    }
`;

/**
 * Global document-level styles. Imported for side-effect from src/index.ts;
 * these apply to anything in light DOM (e.g. <body>, the <app-root> host).
 */
const documentStyles = `
    :root {
        --top-bar-height: 80px;

        /* ---------------- Surface / Material 3 roles ---------------- */
        --surface: #131313;
        --surface-dim: #131313;
        --surface-bright: #393939;
        --surface-container-lowest: #0e0e0e;
        --surface-container-low: #1c1b1b;
        --surface-container: #201f1f;
        --surface-container-high: #2a2a2a;
        --surface-container-highest: #353534;
        --surface-variant: #353534;
        --surface-tint: #c6c6c7;
        --background: #131313;
        --on-background: #e5e2e1;
        --on-surface: #e5e2e1;
        --on-surface-variant: #c4c7c8;
        --inverse-surface: #e5e2e1;
        --inverse-on-surface: #313030;

        /* ---------------- Outlines ---------------- */
        --outline: #8e9192;
        --outline-variant: #444748;

        /* ---------------- Primary (mono white) ---------------- */
        --primary: #ffffff;
        --on-primary: #2f3131;
        --primary-container: #e2e2e2;
        --on-primary-container: #636565;
        --inverse-primary: #5d5f5f;
        --primary-fixed: #e2e2e2;
        --primary-fixed-dim: #c6c6c7;
        --on-primary-fixed: #1a1c1c;
        --on-primary-fixed-variant: #454747;

        /* ---------------- Secondary (kinetic orange) ---------------- */
        --secondary: #ffb691;
        --on-secondary: #552000;
        --secondary-container: #ea6b18;
        --on-secondary-container: #4a1b00;
        --secondary-fixed: #ffdbcb;
        --secondary-fixed-dim: #ffb691;
        --on-secondary-fixed: #341100;
        --on-secondary-fixed-variant: #793100;

        /* ---------------- Tertiary (status green) ---------------- */
        --tertiary: #ffffff;
        --on-tertiary: #003827;
        --tertiary-container: #86f8c8;
        --on-tertiary-container: #007352;
        --tertiary-fixed: #86f8c8;
        --tertiary-fixed-dim: #69dbad;
        --on-tertiary-fixed: #002115;
        --on-tertiary-fixed-variant: #005139;

        /* ---------------- Error ---------------- */
        --error: #ffb4ab;
        --on-error: #690005;
        --error-container: #93000a;
        --on-error-container: #ffdad6;

        /* ---------------- Glass ---------------- */
        --glass-bg: rgba(28, 27, 27, 0.85);
    }

    :root[data-theme="light"] {
        /* ---------------- Surface / Material 3 roles ---------------- */
        --surface: #f8f8f8;
        --surface-dim: #efefef;
        --surface-bright: #ffffff;
        --surface-container-lowest: #ffffff;
        --surface-container-low: #f2f2f2;
        --surface-container: #ebebeb;
        --surface-container-high: #e2e2e2;
        --surface-container-highest: #d9d9d9;
        --surface-variant: #e0e0e0;
        --surface-tint: #5d5f5f;
        --background: #f8f8f8;
        --on-background: #1a1a1a;
        --on-surface: #1a1a1a;
        --on-surface-variant: #444748;
        --inverse-surface: #2f3131;
        --inverse-on-surface: #f0f0f0;

        /* ---------------- Outlines ---------------- */
        --outline: #6e7172;
        --outline-variant: #c4c7c8;

        /* ---------------- Primary (mono black in light) ---------------- */
        --primary: #1a1a1a;
        --on-primary: #ffffff;
        --primary-container: #2f3131;
        --on-primary-container: #e0e0e0;
        --inverse-primary: #c6c6c7;

        /* ---------------- Glass ---------------- */
        --glass-bg: rgba(248, 248, 248, 0.85);
    }

    html, body {
        margin: 0;
        background: var(--surface);
        color: var(--on-surface);
        font-family: 'Inter', sans-serif;
        height: 100%;
    }
    body { overflow: hidden; }
`;

const styleEl = document.createElement('style');
styleEl.textContent = documentStyles;
document.head.appendChild(styleEl);
```

- [ ] **Step 2: Verify the dev server still starts and the app looks identical in dark mode**

```bash
cd web && npm run dev
```

Open `https://localhost:5173` (or whichever port Vite prints). The app should look exactly the same as before — all dark surfaces, white text. If anything is broken (wrong colors, missing tokens), check that the token name in `sharedStyles` matches what you added to `:root`.

- [ ] **Step 3: Commit**

```bash
git add web/src/global-styles.ts
git commit -m "refactor: move CSS color tokens to :root for theme switching support"
```

---

### Task 2: ThemeService

**Files:**
- Create: `web/src/services/theme-service.ts`

**Interfaces:**
- Produces:
  - `themeService.current: 'dark' | 'light'`
  - `themeService.init(): void` — call once at app startup
  - `themeService.toggle(): void` — flips theme, persists, dispatches `theme-change` on `window`
  - `window` event `'theme-change'` with `detail: { theme: 'dark' | 'light' }`

- [ ] **Step 1: Create `web/src/services/theme-service.ts`**

```typescript
class ThemeService {
    private _current: 'dark' | 'light' = 'dark';

    get current(): 'dark' | 'light' {
        return this._current;
    }

    init(): void {
        const saved = localStorage.getItem('fleet-theme');
        this._current = saved === 'light' ? 'light' : 'dark';
        this._apply();
    }

    toggle(): void {
        this._current = this._current === 'dark' ? 'light' : 'dark';
        if (this._current === 'light') {
            localStorage.setItem('fleet-theme', 'light');
        } else {
            localStorage.removeItem('fleet-theme');
        }
        this._apply();
        window.dispatchEvent(
            new CustomEvent<{ theme: 'dark' | 'light' }>('theme-change', {
                detail: { theme: this._current },
            })
        );
    }

    private _apply(): void {
        if (this._current === 'light') {
            document.documentElement.dataset.theme = 'light';
        } else {
            delete document.documentElement.dataset.theme;
        }
    }
}

export const themeService = new ThemeService();
```

- [ ] **Step 2: Smoke-test in the browser console**

With the dev server running, open the browser console and run:

```javascript
// Paste into console after importing — or just verify the module exists
document.documentElement.dataset
// Expected: DOMStringMap {} (no theme key yet)
```

The type-check will confirm correctness in the next step.

- [ ] **Step 3: Type-check**

```bash
cd web && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/services/theme-service.ts
git commit -m "feat: add ThemeService singleton for dark/light toggle"
```

---

### Task 3: Toggle Button in side-nav

**Files:**
- Modify: `web/src/elements/side-nav.ts`

**Interfaces:**
- Consumes: `themeService.init()`, `themeService.toggle()`, `themeService.current` from `web/src/services/theme-service.ts`
- Produces: a sun/moon icon button in the side-nav footer that toggles theme on click and re-renders its icon when `theme-change` fires

- [ ] **Step 1: Add import and state to `side-nav.ts`**

At the top of the file, after the existing imports, add:

```typescript
import { state } from 'lit/decorators.js';
import { themeService } from '../services/theme-service.ts';
```

Then inside the `SideNav` class, add a state property and a bound event handler:

```typescript
@state() private theme: 'dark' | 'light' = 'dark';
private boundOnThemeChange = (e: Event) => {
    this.theme = (e as CustomEvent<{ theme: 'dark' | 'light' }>).detail.theme;
};
```

- [ ] **Step 2: Wire lifecycle hooks**

Replace the existing `connectedCallback` with:

```typescript
connectedCallback() {
    super.connectedCallback();
    this.collapsed = localStorage.getItem('sidebar-collapsed') === 'true';
    themeService.init();
    this.theme = themeService.current;
    window.addEventListener('theme-change', this.boundOnThemeChange);
}
```

Add `disconnectedCallback` after `connectedCallback`:

```typescript
disconnectedCallback() {
    window.removeEventListener('theme-change', this.boundOnThemeChange);
    super.disconnectedCallback();
}
```

- [ ] **Step 3: Add the toggle button to the footer in `render()`**

Replace the existing `<div class="footer">` block with:

```typescript
<div class="footer">
    <button
        class="nav-item theme-toggle"
        title=${this.theme === 'dark' ? msg('Switch to light mode') : msg('Switch to dark mode')}
        @click=${() => themeService.toggle()}
    >
        <span class="material-symbols-outlined">
            ${this.theme === 'dark' ? 'light_mode' : 'dark_mode'}
        </span>
        <span class="label">
            ${this.theme === 'dark' ? msg('Light Mode') : msg('Dark Mode')}
        </span>
    </button>
    <a class="nav-item" href="#/${this.tenantId}/settings"
       title=${this.collapsed ? msg('Support') : ''}>
        <span class="material-symbols-outlined">help</span>
        <span class="label">${msg('Support')}</span>
    </a>
    <a class="nav-item" href="#"
       title=${this.collapsed ? msg('Sign Out') : ''}
       @click=${(e: Event) => { e.preventDefault(); logout(); }}>
        <span class="material-symbols-outlined">logout</span>
        <span class="label">${msg('Sign Out')}</span>
    </a>
</div>
```

- [ ] **Step 4: Add `.theme-toggle` CSS to side-nav styles**

Inside the `css\`` block in `static styles`, add after the existing `.footer` rule:

```css
button.theme-toggle {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 16px;
    border-radius: var(--radius-md);
    color: var(--on-surface-variant);
    text-decoration: none;
    transition: background 0.15s ease, color 0.15s ease;
    font: var(--type-label-caps);
    letter-spacing: 0.05em;
    text-transform: uppercase;
}
button.theme-toggle:hover {
    background: var(--surface-container-high);
    color: var(--primary);
}
:host([collapsed]) button.theme-toggle {
    justify-content: center;
    padding: 12px 0;
    gap: 0;
}
:host([collapsed]) button.theme-toggle span.label {
    display: none;
}
```

- [ ] **Step 5: Verify in browser**

Open the app. The side-nav footer should show a "Light Mode" button with a sun icon above Support and Sign Out. Clicking it should instantly switch all surfaces to light colors. Clicking again returns to dark. Reload — the chosen theme should persist.

- [ ] **Step 6: Type-check**

```bash
cd web && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add web/src/elements/side-nav.ts
git commit -m "feat: add light/dark theme toggle to side-nav footer"
```

---

### Task 4: Map Tile Switching

**Files:**
- Modify: `web/src/views/fleet-overview.ts`
- Modify: `web/src/elements/vehicle-trips-panel.ts`
- Modify: `web/src/elements/trip-replay-modal.ts`

**Interfaces:**
- Consumes: `themeService.current` and `window` event `'theme-change'` with `detail: { theme: 'dark' | 'light' }` (produced by Task 2)

#### 4a — fleet-overview.ts

- [ ] **Step 1: Add import and field**

At the top of `web/src/views/fleet-overview.ts`, after existing imports add:

```typescript
import { themeService } from '../services/theme-service.ts';
```

Inside the `FleetOverviewView` class, after the existing private fields (near `private leafletMap`), add:

```typescript
private tileLayer: L.TileLayer | null = null;
private boundOnThemeChange = (e: Event) => {
    const { theme } = (e as CustomEvent<{ theme: 'dark' | 'light' }>).detail;
    this.updateTileLayer(theme);
};
```

- [ ] **Step 2: Add `buildTileLayer()` and `updateTileLayer()` methods**

Add these two methods to the class (place them near other private map methods such as `centerMap`):

```typescript
private buildTileLayer(theme: 'dark' | 'light'): L.TileLayer {
    const url = theme === 'light'
        ? 'https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png'
        : 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png';
    return L.tileLayer(url, {
        attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>',
        subdomains: 'abcd',
        maxZoom: 19,
    });
}

private updateTileLayer(theme: 'dark' | 'light'): void {
    if (!this.leafletMap) return;
    this.tileLayer?.remove();
    this.tileLayer = this.buildTileLayer(theme);
    this.tileLayer.addTo(this.leafletMap);
    const mapEl = this.renderRoot.querySelector<HTMLElement>('.map');
    if (mapEl) mapEl.classList.toggle('dark-tiles', theme === 'dark');
}
```

- [ ] **Step 3: Update `firstUpdated()` to use the new methods**

In `firstUpdated()`, replace the existing `L.tileLayer(...).addTo(this.leafletMap)` call:

```typescript
// Remove this:
L.tileLayer('https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png', {
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>',
    subdomains: 'abcd',
    maxZoom: 19,
}).addTo(this.leafletMap);
```

Replace with:

```typescript
this.tileLayer = this.buildTileLayer(themeService.current);
this.tileLayer.addTo(this.leafletMap);
const mapEl = this.renderRoot.querySelector<HTMLElement>('.map');
if (mapEl && themeService.current === 'dark') mapEl.classList.add('dark-tiles');
window.addEventListener('theme-change', this.boundOnThemeChange);
```

- [ ] **Step 4: Remove the listener in `disconnectedCallback()`**

In the existing `disconnectedCallback`, add before `super.disconnectedCallback()`:

```typescript
window.removeEventListener('theme-change', this.boundOnThemeChange);
```

- [ ] **Step 5: Update tile brightness CSS**

In the `fleet-overview.ts` static styles, find:

```css
/* Lift the very-dark CARTO tiles to a readable contrast level */
.map .leaflet-tile {
    filter: brightness(1.8);
}
```

Replace with:

```css
/* Brightness boost only for dark CARTO tiles */
.map.dark-tiles .leaflet-tile {
    filter: brightness(1.8);
}
```

#### 4b — vehicle-trips-panel.ts

- [ ] **Step 6: Add import, field, and handler to `vehicle-trips-panel.ts`**

After existing imports, add:

```typescript
import { themeService } from '../services/theme-service.ts';
```

Inside the `VehicleTripsPanel` class, after the existing private fields, add:

```typescript
private tileLayer: L.TileLayer | null = null;
private boundOnThemeChange = (e: Event) => {
    const { theme } = (e as CustomEvent<{ theme: 'dark' | 'light' }>).detail;
    if (!this.map) return;
    this.tileLayer?.remove();
    this.tileLayer = this.buildTileLayer(theme);
    this.tileLayer.addTo(this.map);
};
```

Add `buildTileLayer` method to the class:

```typescript
private buildTileLayer(theme: 'dark' | 'light'): L.TileLayer {
    const url = theme === 'light'
        ? 'https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png'
        : 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png';
    return L.tileLayer(url, {
        attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>',
        subdomains: 'abcd',
        maxZoom: 19,
    });
}
```

- [ ] **Step 7: Update lifecycle in `vehicle-trips-panel.ts`**

In `connectedCallback`, add after the existing `this.unsubscribePrefs = ...` line:

```typescript
window.addEventListener('theme-change', this.boundOnThemeChange);
```

In `disconnectedCallback`, add before `super.disconnectedCallback()`:

```typescript
window.removeEventListener('theme-change', this.boundOnThemeChange);
```

- [ ] **Step 8: Update `firstUpdated()` in `vehicle-trips-panel.ts`**

In `firstUpdated()`, replace:

```typescript
L.tileLayer('https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png', {
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>',
    subdomains: 'abcd',
    maxZoom: 19,
}).addTo(this.map);
```

With:

```typescript
this.tileLayer = this.buildTileLayer(themeService.current);
this.tileLayer.addTo(this.map);
```

#### 4c — trip-replay-modal.ts

- [ ] **Step 9: Add import, remove module-level constant, add field and handler to `trip-replay-modal.ts`**

After existing imports, add:

```typescript
import { themeService } from '../services/theme-service.ts';
```

Remove the module-level constant (keep `TILE_ATTRIBUTION`):

```typescript
// Delete this line only:
const TILE_URL = 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png';
```

Inside the `TripReplayModal` class (after existing private fields), add:

```typescript
private tileLayer: L.TileLayer | null = null;
private boundOnThemeChange = (e: Event) => {
    const { theme } = (e as CustomEvent<{ theme: 'dark' | 'light' }>).detail;
    if (!this.map) return;
    this.tileLayer?.remove();
    this.tileLayer = this.buildTileLayer(theme);
    this.tileLayer.addTo(this.map);
};

private buildTileLayer(theme: 'dark' | 'light'): L.TileLayer {
    const url = theme === 'light'
        ? 'https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png'
        : 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png';
    return L.tileLayer(url, {
        attribution: TILE_ATTRIBUTION,
        subdomains: 'abcd',
        maxZoom: 19,
    });
}
```

- [ ] **Step 10: Wire lifecycle and update BOTH tile init sites in `trip-replay-modal.ts`**

The modal initializes its map in two different methods depending on data availability: `initFallbackMap()` (sparse GPS data) and `initMap()` (full replay). Both must use the stored tile reference.

**In the existing `connectedCallback`**, add after `document.addEventListener('keydown', this.onKeydown)`:

```typescript
window.addEventListener('theme-change', this.boundOnThemeChange);
```

**In the existing `disconnectedCallback`**, add after `document.removeEventListener('keydown', this.onKeydown)`:

```typescript
window.removeEventListener('theme-change', this.boundOnThemeChange);
```

**In `initFallbackMap()`**, replace:

```typescript
L.tileLayer(TILE_URL, { attribution: TILE_ATTRIBUTION, subdomains: 'abcd', maxZoom: 19 }).addTo(this.map);
```

With:

```typescript
this.tileLayer = this.buildTileLayer(themeService.current);
this.tileLayer.addTo(this.map);
```

**In `initMap()`**, replace:

```typescript
L.tileLayer(TILE_URL, { attribution: TILE_ATTRIBUTION, subdomains: 'abcd', maxZoom: 19 }).addTo(this.map);
```

With:

```typescript
this.tileLayer = this.buildTileLayer(themeService.current);
this.tileLayer.addTo(this.map);
```

- [ ] **Step 11: Type-check all three files**

```bash
cd web && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 12: Verify map tile switching**

Open the app with the dev server. Toggle to light mode via the side-nav button. All three map surfaces (fleet overview, vehicle trips panel, trip replay modal) should switch to the pale CARTO light tiles. Toggle back — dark tiles return.

- [ ] **Step 13: Commit**

```bash
git add web/src/views/fleet-overview.ts \
        web/src/elements/vehicle-trips-panel.ts \
        web/src/elements/trip-replay-modal.ts
git commit -m "feat: swap Leaflet tile layer on theme change in all three map components"
```
