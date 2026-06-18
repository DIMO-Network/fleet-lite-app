# Marker Clustering for Fleet Map View

**Date:** 2026-06-17
**Status:** Approved

## Summary

Add marker clustering to the fleet overview map so nearby vehicle dots consolidate into a numbered cluster dot at low zoom levels. Clicking a cluster zooms in; at max zoom, clusters spiderify to reveal overlapping markers.

## Approach

Use `leaflet.markercluster` — the standard Leaflet clustering plugin. It wraps individual layers in a `MarkerClusterGroup` that automatically reclusters on zoom change and handles zoom-to-bounds + spiderify on click. No custom clustering algorithm required.

## Architecture

### New dependency

```
leaflet.markercluster
@types/leaflet.markercluster
```

Import `MarkerCluster.css` inline (animation/positioning base). Skip `MarkerCluster.Default.css` — replaced by custom icon styling.

### New field

```typescript
private clusterGroup: L.MarkerClusterGroup | null = null;
```

### Changed methods

| Method | Change |
|--------|--------|
| `firstUpdated()` | Create `clusterGroup` with custom `iconCreateFunction`; add to map before `placeMarkers()` |
| `addMarkers()` | `marker.addTo(this.leafletMap)` → `this.clusterGroup.addLayer(marker)` |
| `placeMarkers()` | `this.markers.forEach(m => m.remove())` → `this.clusterGroup.clearLayers()` |
| `zoomToVehicle()` | `clusterGroup.zoomToShowLayer(marker, callback)` so target vehicles inside collapsed clusters are revealed before the quick-view opens |
| `disconnectedCallback()` | Null out `clusterGroup` reference after `leafletMap.remove()` |

### Unchanged

- `this.markers` Map — still tracks individual `L.CircleMarker` per tokenId
- `centerMap()` — still derives bounds from `this.markers.values()`
- `visibleCards()` / `visibleTokenIds()` — filter logic unchanged
- Progressive streaming — `addMarkers()` is still called per batch; MarkerClusterGroup reclusters incrementally as layers are added
- `openQuickView()` selected/hover marker styles — manipulate the underlying `CircleMarker` directly, no change needed

## Cluster Icon Styling

Custom `iconCreateFunction` returns a `L.DivIcon` styled to match the dark CARTO tile theme:

- **Color:** `#69dbad` (same teal as individual vehicle dots)
- **Border:** `#1a2332` dark ring, 2px
- **Outer ring:** semi-transparent teal at 30% opacity, 6px larger than the dot (visual distinction from single markers)
- **Text:** dark (`#1a2332`), centered, no font-weight adjustment needed at this size
- **Sizes:**
  - Small (< 10 vehicles): 32×32px
  - Medium (< 50 vehicles): 40×40px
  - Large (50+ vehicles): 48×48px

## Click Behavior

Both behaviors are default markercluster behavior — no custom code needed:

- **Spread-out cluster:** click zooms to the cluster's bounding box, revealing individual dots
- **Overlapping cluster at max zoom:** click spiderifies — individual markers fan out around the cluster point

## Scope

- `web/package.json` — add `leaflet.markercluster` and `@types/leaflet.markercluster`
- `web/src/views/fleet-overview.ts` — ~30 lines changed across 5 methods + new CSS import + cluster icon function
- No changes to services, types, API layer, or list panel
