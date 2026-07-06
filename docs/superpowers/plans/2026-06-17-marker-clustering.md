# Marker Clustering Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `leaflet.markercluster` to the fleet map so nearby vehicle dots consolidate into numbered cluster dots at low zoom, with zoom-to-bounds on click and spiderify at max zoom.

**Architecture:** Replace direct `marker.addTo(map)` calls with a `MarkerClusterGroup` layer. The cluster group sits between individual `L.CircleMarker` instances and the map, reclustering automatically on zoom. All existing marker state (`this.markers` Map, hover/selected styles, progressive streaming) remains unchanged.

**Tech Stack:** Leaflet 1.9.x, `leaflet.markercluster`, TypeScript, Lit (web components, shadow DOM)

## Global Constraints

- All code is TypeScript — no `any` types, no `@ts-ignore`
- Lit shadow DOM requires CSS injected via `unsafeCSS()` in `static styles` — no external stylesheets
- Type-check command: `cd web && npx tsc --noEmit`
- Dev server: `cd web && npm run dev`
- No unit test framework exists — verification is `tsc --noEmit` + manual visual check

---

### Task 1: Install packages and wire imports

**Files:**
- Modify: `web/package.json`
- Modify: `web/src/views/fleet-overview.ts` (lines 1–10, 29–30, 502–505)

**Interfaces:**
- Produces: `this.clusterGroup: L.MarkerClusterGroup | null` field and `clusterIcon()` helper available to Task 2

- [ ] **Step 1: Install packages**

```bash
cd web && npm install leaflet.markercluster && npm install --save-dev @types/leaflet.markercluster
```

Expected output: both packages appear in `node_modules/`, `package.json` updated.

- [ ] **Step 2: Add imports to fleet-overview.ts**

After line 5 (`import leafletCss from 'leaflet/dist/leaflet.css?inline';`), add:

```typescript
import 'leaflet.markercluster';
import markerClusterCss from 'leaflet.markercluster/dist/MarkerCluster.css?inline';
```

- [ ] **Step 3: Add clusterGroup field**

After line 30 (`private markers = new Map<string, L.CircleMarker>();`), add:

```typescript
private clusterGroup: L.MarkerClusterGroup | null = null;
```

- [ ] **Step 4: Add cluster icon helper**

Add this private method directly above `firstUpdated()` (before line 429):

```typescript
private clusterIcon(count: number): L.DivIcon {
    const size = count < 10 ? 32 : count < 50 ? 40 : 48;
    const total = size + 12;
    return L.divIcon({
        html: `<div style="width:${size}px;height:${size}px;background:#69dbad;border:2px solid #1a2332;border-radius:50%;box-shadow:0 0 0 6px rgba(105,219,173,0.25);display:flex;align-items:center;justify-content:center;font-size:${size < 40 ? 11 : 13}px;font-weight:600;color:#1a2332;">${count}</div>`,
        className: '',
        iconSize: [total, total],
        iconAnchor: [total / 2, total / 2],
    });
}
```

- [ ] **Step 5: Initialize clusterGroup in firstUpdated()**

Inside `firstUpdated()`, after `this.leafletMap = L.map(el, {...}).setView([39.5, -98.35], 4);` and after the `L.tileLayer(...)` call but before `this.resizeObserver = ...`, add:

```typescript
this.clusterGroup = L.markerClusterGroup({
    iconCreateFunction: (cluster) => this.clusterIcon(cluster.getChildCount()),
    showCoverageOnHover: false,
    zoomToBoundsOnClick: true,
    spiderfyOnMaxZoom: true,
    maxClusterRadius: 60,
    animate: true,
});
this.clusterGroup.addTo(this.leafletMap);
```

- [ ] **Step 6: Add markerClusterCss to static styles**

In `static styles = [...]` (around line 502), add `unsafeCSS(markerClusterCss)` after `unsafeCSS(leafletCss)`:

```typescript
static styles = [
    sharedStyles,
    unsafeCSS(leafletCss),
    unsafeCSS(markerClusterCss),
    css`
        // ... rest unchanged
```

- [ ] **Step 7: Type-check**

```bash
cd web && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add web/package.json web/package-lock.json web/src/views/fleet-overview.ts
git commit -m "feat(map): install leaflet.markercluster and initialize cluster group"
```

---

### Task 2: Wire all marker operations through the cluster group

**Files:**
- Modify: `web/src/views/fleet-overview.ts` (methods: `addMarkers`, `placeMarkers`, `zoomToVehicle`, `disconnectedCallback`)

**Interfaces:**
- Consumes: `this.clusterGroup: L.MarkerClusterGroup` from Task 1

- [ ] **Step 1: Update addMarkers() to use clusterGroup**

In `addMarkers()` (around line 216), replace:

```typescript
marker.addTo(this.leafletMap);
```

with:

```typescript
this.clusterGroup!.addLayer(marker);
```

- [ ] **Step 2: Update placeMarkers() to use clusterGroup**

In `placeMarkers()` (around line 224), replace:

```typescript
this.markers.forEach((m) => m.remove());
```

with:

```typescript
this.clusterGroup!.clearLayers();
```

- [ ] **Step 3: Update zoomToVehicle() to reveal clustered markers**

Replace the entire body of `zoomToVehicle()` (lines 75–84):

```typescript
private zoomToVehicle(e: Event, tokenId: string) {
    e.preventDefault();
    e.stopPropagation();
    const marker = this.markers.get(tokenId);
    if (!marker || !this.leafletMap || !this.clusterGroup) return;
    if (window.innerWidth < 768) {
        this.panelCollapsed = true;
    }
    this.clusterGroup.zoomToShowLayer(marker, () => {
        this.leafletMap!.flyTo(marker.getLatLng(), Math.max(this.leafletMap!.getZoom(), 14));
    });
}
```

- [ ] **Step 4: Update disconnectedCallback() to null clusterGroup**

In `disconnectedCallback()` (around lines 457–468), after `this.leafletMap?.remove(); this.leafletMap = null;`, add:

```typescript
this.clusterGroup = null;
```

- [ ] **Step 5: Type-check**

```bash
cd web && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 6: Visual verification**

```bash
cd web && npm run dev
```

Open the fleet map view and verify:
1. At low zoom (country level), nearby vehicle dots consolidate into a single teal dot with a count number
2. Clicking a cluster zooms in to its bounding box, splitting it into smaller clusters or individual dots
3. At max zoom with overlapping vehicles, clicking a cluster spiderifies (markers fan out)
4. Clicking an individual dot still opens the quick-view panel
5. The zoom-to button on a list card still flies to and reveals the vehicle even if it's inside a cluster
6. Progressive streaming still works — markers appear incrementally and cluster as they arrive
7. Group/search filter changes re-place markers correctly (clusters reflect filtered set)

- [ ] **Step 7: Commit**

```bash
git add web/src/views/fleet-overview.ts
git commit -m "feat(map): cluster nearby vehicle markers with leaflet.markercluster"
```
