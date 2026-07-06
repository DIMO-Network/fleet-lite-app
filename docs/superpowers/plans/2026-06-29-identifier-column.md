# Identifier Column Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the aftermarket device number display in map view and list view with license plate / VIN identifiers, and add a Glovebox deep-link icon for vehicles with neither.

**Architecture:** Four files change. The Glovebox view gains an `initialTokenId` property and the router gains a matching `/:tenantId/glovebox/:tokenId` route. The list view `toCard` mapper populates `licensePlate`/`vin` fields (already on `VehicleCard`) and the cell template is rewritten. The map view quick-panel removes the integration-string fallback and adds the glovebox link. No new abstractions.

**Tech Stack:** Lit 3, `@lit-labs/router`, TypeScript, Material Symbols icon font, CSS custom properties from the existing design system.

## Global Constraints

- Use `msg()` from `@lit/localize` for every user-visible string
- Icon names must be from the Material Symbols Outlined set already loaded in the app
- CSS custom properties: `--surface-container-high`, `--outline-variant`, `--radius-sm`, `--type-label-caps`, `--type-body-sm`, `--on-surface-variant`, `--primary`, `--font-mono`
- Navigation uses hash-based URLs: `location.hash = '#/...'`; links use `href="#/..."`
- The more-specific glovebox route (`/:tenantId/glovebox/:tokenId`) must be registered **before** `/:tenantId/glovebox` in the router array

---

## Files

| File | Change |
|------|--------|
| `web/src/elements/app-root.ts` | Add `/:tenantId/glovebox/:tokenId` route |
| `web/src/views/glovebox.ts` | Add `initialTokenId` property; pre-select vehicle on load |
| `web/src/views/fleet-list-view.ts` | Populate `licensePlate`/`vin` in `toCard`; rewrite identifier cell; rename column |
| `web/src/views/fleet-overview.ts` | Remove integration-string fallback; add Glovebox link when no plate/VIN |

---

### Task 1: Glovebox deep-link route + `initialTokenId` property

**Files:**
- Modify: `web/src/elements/app-root.ts:64-73`
- Modify: `web/src/views/glovebox.ts:34-62`

**Interfaces:**
- Produces: `<glovebox-view .initialTokenId="3681">` — string token ID, pre-selects vehicle with matching `tokenId`; falls back to first vehicle if not found

- [ ] **Step 1: Add `initialTokenId` property to GloveboxView**

In `web/src/views/glovebox.ts`, after line 36 (`@property({ type: String }) tenantId = '';`), add:

```typescript
@property({ type: String }) initialTokenId = '';
```

Then replace the `connectedCallback` body (lines 49–62) with:

```typescript
async connectedCallback() {
    super.connectedCallback();
    try {
        const res = await ApiService.getInstance().get<VehiclesResponse>('/vehicles');
        this.vehicles = res.vehicles || [];
        const initial = this.initialTokenId
            ? this.vehicles.find(v => String(v.tokenId) === this.initialTokenId)
            : null;
        this.selected = initial ?? this.vehicles[0] ?? null;
        if (this.selected) {
            await this.loadDocs(this.selected.tokenId);
        }
    } catch (e) {
        console.error('Failed to load vehicles', e);
    } finally {
        this.loadingVehicles = false;
    }
}
```

- [ ] **Step 2: Add the deep-link route in app-root.ts**

In `web/src/elements/app-root.ts`, inside the `Routes` array in the constructor, add the new route **before** the existing `/:tenantId/glovebox` entry (currently line 70):

```typescript
{ path: '/:tenantId/glovebox/:tokenId', render: ({ tokenId }) => html`<glovebox-view .tenantId=${this.tenantId} .initialTokenId=${tokenId}></glovebox-view>` },
```

The two glovebox entries should now read (order matters — specific before general):

```typescript
{ path: '/:tenantId/glovebox/:tokenId', render: ({ tokenId }) => html`<glovebox-view .tenantId=${this.tenantId} .initialTokenId=${tokenId}></glovebox-view>` },
{ path: '/:tenantId/glovebox',          render: () => html`<glovebox-view .tenantId=${this.tenantId}></glovebox-view>` },
```

- [ ] **Step 3: Manual verify**

Build the app (`cd web && npm run build` or dev server `npm run dev`). Navigate to `#/<tenantId>/glovebox/<validTokenId>` in the browser. Confirm the Glovebox opens with that vehicle pre-selected in the vehicle list. Also navigate to `#/<tenantId>/glovebox` (no tokenId) and confirm it still opens with the first vehicle selected.

- [ ] **Step 4: Commit**

```bash
git add web/src/elements/app-root.ts web/src/views/glovebox.ts
git commit -m "feat: add glovebox deep-link route with initialTokenId pre-selection"
```

---

### Task 2: List view — Identifier column

**Files:**
- Modify: `web/src/views/fleet-list-view.ts`

**Interfaces:**
- Consumes: `VehicleCard.licensePlate?: string`, `VehicleCard.vin?: string` (already defined in `web/src/types/vehicle.ts:61,63`)
- Consumes: `this.tenantId: string` (already on `FleetListView`)

- [ ] **Step 1: Populate `licensePlate` and `vin` in `toCard`**

In `web/src/views/fleet-list-view.ts`, the `toCard` method returns an object starting around line 53. Add `licensePlate` and `vin` to the returned object:

```typescript
private toCard(v: Vehicle): VehicleCard {
    const hasSynthetic = !!(v.syntheticDevice && v.syntheticDevice.tokenId > 0);
    const hasAftermarket = !!(v.aftermarketDevice && v.aftermarketDevice.tokenId > 0);
    const integrated = hasSynthetic || hasAftermarket;
    const integration = hasAftermarket
        ? `Aftermarket #${v.aftermarketDevice!.tokenId}`
        : hasSynthetic
            ? `Synthetic #${v.syntheticDevice.tokenId}`
            : '';
    return {
        tokenId: String(v.tokenId),
        make: v.definition.make,
        title: this.formatTitle(v),
        location: integration,
        seenAt: `Token #${v.tokenId}`,
        online: integrated,
        errorMessage: integrated ? undefined : msg('No DIMO integration — pair a device to stream telemetry'),
        isFavorite: v.isFavorite ?? false,
        groups: v.groups ?? [],
        licensePlate: v.licensePlate,
        vin: v.vin || undefined,
    };
}
```

- [ ] **Step 2: Rename column header from "Integration" to "Identifier"**

Find line 373:
```typescript
<th class="col-integration">${msg('Integration')}</th>
```

Replace with:
```typescript
<th class="col-identifier">${msg('Identifier')}</th>
```

- [ ] **Step 3: Rewrite the identifier cell (lines ~254-258)**

Find:
```typescript
<td class="col-integration">
    ${v.online
        ? html`<span class="integration-badge">${v.location}</span>`
        : html`<span class="offline-label">${msg('No integration')}</span>`}
</td>
```

Replace with:
```typescript
<td class="col-identifier">
    ${v.licensePlate ? html`
        <span class="identifier-plate">
            <span class="material-symbols-outlined">directions_car</span>${v.licensePlate}
        </span>
    ` : nothing}
    ${v.vin ? html`
        <span class="identifier-vin">${v.vin}</span>
    ` : nothing}
    ${!v.licensePlate && !v.vin ? html`
        <a class="upload-id-btn"
           href="#/${this.tenantId}/glovebox/${v.tokenId}"
           title=${msg('Upload vehicle documents to identify this vehicle')}>
            <span class="material-symbols-outlined">inventory_2</span>
        </a>
    ` : nothing}
</td>
```

- [ ] **Step 4: Update CSS — rename and replace integration styles**

In the CSS block (around lines 554–618), make these changes:

Replace:
```css
.col-integration { width: 180px; }
```
With:
```css
.col-identifier { width: 180px; }
```

Replace the entire `/* ── Integration badge */` block (`.integration-badge` and `.offline-label`) with:

```css
/* ── Identifier cell ─────────────────────────────────── */
.identifier-plate {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    background: var(--surface-container-high);
    border: 1px solid var(--outline-variant);
    border-radius: var(--radius-sm);
    padding: 3px 8px;
    font: var(--type-label-caps);
    letter-spacing: 0.04em;
    color: var(--on-surface-variant);
    white-space: nowrap;
}
.identifier-plate .material-symbols-outlined { font-size: 14px; }

.identifier-vin {
    display: block;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--on-surface-variant);
    margin-top: 4px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 148px;
}

.upload-id-btn {
    display: inline-flex;
    align-items: center;
    color: var(--on-surface-variant);
    opacity: 0.4;
    text-decoration: none;
    transition: opacity 0.15s, color 0.15s;
}
.upload-id-btn:hover { opacity: 1; color: var(--primary); }
.upload-id-btn .material-symbols-outlined { font-size: 20px; }
```

- [ ] **Step 5: Manual verify**

Start dev server. Open the list view (`/stats`). Confirm:
- Vehicles with a license plate show the plate badge with car icon
- Vehicles with a VIN show it below the plate in mono text
- Vehicles with both show plate + VIN stacked
- Vehicles with neither show the `inventory_2` icon
- Clicking the icon navigates to `#/<tenantId>/glovebox/<tokenId>` and pre-selects that vehicle

- [ ] **Step 6: Commit**

```bash
git add web/src/views/fleet-list-view.ts
git commit -m "feat: replace integration column with identifier (plate/VIN) in list view"
```

---

### Task 3: Map view — remove aftermarket fallback, add Glovebox link

**Files:**
- Modify: `web/src/views/fleet-overview.ts`

**Interfaces:**
- Consumes: `VehicleCard.licensePlate?: string`, `VehicleCard.vin?: string` (already set in `fleet-overview.ts toCard`)
- Consumes: `this.tenantId: string` (already on `FleetOverviewView`)

- [ ] **Step 1: Replace the integration-string fallback in the quick-view panel**

In `web/src/views/fleet-overview.ts`, find this line (around line 1383) inside the quick-view panel template:

```typescript
${!v.vin && v.location ? html`<p class="location">${v.location}</p>` : ''}
```

Replace with:

```typescript
${!v.licensePlate && !v.vin ? html`
    <a class="upload-id-link" href="#/${this.tenantId}/glovebox/${v.tokenId}"
       title=${msg('Upload vehicle documents to identify this vehicle')}>
        <span class="material-symbols-outlined">inventory_2</span>
        <span>${msg('Add vehicle ID')}</span>
    </a>
` : ''}
```

- [ ] **Step 2: Add CSS for the upload link**

In the `fleet-overview.ts` CSS block, add after the `.vin-value` styles (around the `.vehicle-meta .vin-line` block, roughly line 1220):

```css
.vehicle-meta .upload-id-link {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin-top: 4px;
    font: var(--type-body-sm);
    color: var(--on-surface-variant);
    opacity: 0.5;
    text-decoration: none;
    transition: opacity 0.15s, color 0.15s;
}
.vehicle-meta .upload-id-link:hover { opacity: 1; color: var(--primary); }
.vehicle-meta .upload-id-link .material-symbols-outlined { font-size: 16px; }
```

- [ ] **Step 3: Manual verify**

Open the map view. Click a vehicle marker to open the quick-view panel. Confirm:
- Vehicles with a plate: plate shows, no aftermarket number
- Vehicles with a VIN: VIN shows, no aftermarket number
- Vehicles with both: plate and VIN both show
- Vehicles with neither plate nor VIN: `inventory_2` icon + "Add vehicle ID" text appears as a link; clicking navigates to Glovebox pre-selecting that vehicle

- [ ] **Step 4: Commit**

```bash
git add web/src/views/fleet-overview.ts
git commit -m "feat: remove aftermarket number from map quick-view, add glovebox link for unidentified vehicles"
```
