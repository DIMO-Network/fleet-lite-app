# Collapsible Sidebar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a collapsible sidebar that animates between 280px (full labels) and 56px (icons only), toggled via a tab on the right edge, with state persisted to localStorage.

**Architecture:** All changes are self-contained in `side-nav.ts`. A `collapsed` boolean property reflects to a host attribute so collapsed-state CSS can use `:host([collapsed])` selectors. The toggle tab is absolutely positioned and sticks 12px past the sidebar's right border.

**Tech Stack:** Lit 3, CSS custom properties, localStorage

## Global Constraints

- No changes to `app-root.ts` or `global-styles.ts` — sidebar resize must work via the existing flex layout.
- `--sidebar-width: 280px` is defined in `global-styles.ts` at line 89 but is only used in `side-nav.ts` — no need to update it, the collapsed width is hardcoded to 56px in `:host([collapsed])`.
- Collapsed state localStorage key: `sidebar-collapsed` (string `"true"` / `"false"`).
- Chevron icon: Material Symbols `chevron_left` (expanded → clicking collapses) and `chevron_right` (collapsed → clicking expands).
- Tooltip (`title` attribute) on each nav item and footer item when collapsed, so icon-only items are discoverable.
- No external dependencies.

---

### Task 1: Add collapsed property and localStorage init

**Files:**
- Modify: `web/src/elements/side-nav.ts`

**Interfaces:**
- Produces: `collapsed: boolean` reactive property that reflects to the `collapsed` HTML attribute

- [ ] **Step 1: Add the `collapsed` property with attribute reflection**

In `side-nav.ts`, add the import for `state` decorator and add the property inside the class, right after the existing `@property` declarations:

```ts
import { LitElement, html, css } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property } from 'lit/decorators.js';
```

Change the import to also include `state` — but actually `collapsed` needs to reflect to attribute for CSS, so use `@property` with `reflect`:

```ts
@property({ type: Boolean, reflect: true }) collapsed = false;
```

Place this after the existing `@property({ type: String }) tenantId = '';` line.

- [ ] **Step 2: Initialize from localStorage in `connectedCallback`**

Add this method to the class (after the `static styles` block, before `private item(...)`):

```ts
connectedCallback() {
    super.connectedCallback();
    this.collapsed = localStorage.getItem('sidebar-collapsed') === 'true';
}
```

- [ ] **Step 3: Add the toggle method**

Add this method directly after `connectedCallback`:

```ts
private toggle() {
    this.collapsed = !this.collapsed;
    localStorage.setItem('sidebar-collapsed', String(this.collapsed));
}
```

- [ ] **Step 4: Verify manually**

Run the dev server (`cd web && npm run dev`), open the app. Open DevTools console and run:
```js
document.querySelector('side-nav').collapsed
// expected: false (or true if localStorage has "true")
localStorage.setItem('sidebar-collapsed', 'true');
location.reload();
document.querySelector('side-nav').collapsed
// expected: true
```
Clean up: `localStorage.removeItem('sidebar-collapsed')`.

- [ ] **Step 5: Commit**

```bash
git add web/src/elements/side-nav.ts
git commit -m "feat(sidebar): add collapsed property with localStorage persistence"
```

---

### Task 2: Add toggle tab and collapsed-mode CSS

**Files:**
- Modify: `web/src/elements/side-nav.ts`

**Interfaces:**
- Consumes: `collapsed` property and `toggle()` method from Task 1
- Produces: visual toggle tab rendered in the sidebar; full collapsed/expanded CSS

- [ ] **Step 1: Add `:host` `position: relative` and `overflow: visible` + width transition**

Find the `:host` block in `static styles` and update it:

```css
:host {
    display: flex;
    flex-direction: column;
    width: var(--sidebar-width);
    height: 100vh;
    padding: var(--stack-md);
    background: var(--surface-container-low);
    border-right: 1px solid var(--outline-variant);
    flex-shrink: 0;
    z-index: 50;
    position: relative;
    overflow: visible;
    transition: width 0.2s ease, padding 0.2s ease;
}
```

- [ ] **Step 2: Add collapsed host state overrides**

Add this block immediately after the `:host { ... }` block (before the `@media` rule):

```css
:host([collapsed]) {
    width: 56px;
    padding: var(--stack-md) 0;
}
```

- [ ] **Step 3: Hide brand text in collapsed mode**

After the existing `.brand h1 { ... }` and `.brand p { ... }` blocks, add:

```css
:host([collapsed]) .brand {
    justify-content: center;
    padding: var(--stack-md) 0;
    margin-bottom: 32px;
    gap: 0;
}
:host([collapsed]) .brand h1,
:host([collapsed]) .brand p {
    display: none;
}
```

- [ ] **Step 4: Collapsed nav item styles — icons centered, labels hidden**

Add after the existing `a.nav-item.active .material-symbols-outlined { ... }` block:

```css
:host([collapsed]) a.nav-item {
    justify-content: center;
    padding: 12px 0;
    gap: 0;
}
:host([collapsed]) a.nav-item span.label {
    display: none;
}
```

- [ ] **Step 5: Toggle tab styles**

Add these new rules to the `css` block (before the closing backtick):

```css
.collapse-toggle {
    position: absolute;
    right: -12px;
    top: 50%;
    transform: translateY(-50%);
    width: 24px;
    height: 48px;
    background: var(--surface-container-high);
    border: 1px solid var(--outline-variant);
    border-left: none;
    border-radius: 0 var(--radius-md) var(--radius-md) 0;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    z-index: 51;
    color: var(--on-surface-variant);
    transition: background 0.15s ease, color 0.15s ease;
}
.collapse-toggle:hover {
    background: var(--surface-container-highest);
    color: var(--primary);
}
.collapse-toggle .material-symbols-outlined {
    font-size: 18px;
}
```

- [ ] **Step 6: Render the toggle tab in the template**

In the `render()` method, add the toggle tab as the first child inside the returned template (before `<div class="brand">`):

```ts
render() {
    return html`
        <button class="collapse-toggle" @click=${this.toggle} title=${this.collapsed ? msg('Expand sidebar') : msg('Collapse sidebar')}>
            <span class="material-symbols-outlined">
                ${this.collapsed ? 'chevron_right' : 'chevron_left'}
            </span>
        </button>
        <div class="brand">
        ...
```

- [ ] **Step 7: Add tooltips to nav items and footer items when collapsed**

Update the `private item(...)` method to add a `title` attribute when collapsed:

```ts
private item(key: NavKey, icon: string, label: string, suffix: string) {
    const cls = this.active === key ? 'nav-item active' : 'nav-item';
    const href = `#/${this.tenantId}${suffix}`;
    return html`
        <a class=${cls} href=${href} title=${this.collapsed ? label : ''}>
            <span class="material-symbols-outlined">${icon}</span>
            <span class="label">${label}</span>
        </a>
    `;
}
```

Also update the two footer items in `render()` to add titles when collapsed:

```ts
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
```

- [ ] **Step 8: Verify manually**

With the dev server running:
1. Click the chevron tab on the sidebar edge → sidebar should animate to 56px, showing only icons
2. Hover each icon → tooltip should appear with the nav item name
3. Click the tab again → sidebar should expand back to 280px with labels
4. Reload the page → sidebar should stay in the last state used

- [ ] **Step 9: Commit**

```bash
git add web/src/elements/side-nav.ts
git commit -m "feat(sidebar): collapsible icon-only mode with edge toggle tab"
```
