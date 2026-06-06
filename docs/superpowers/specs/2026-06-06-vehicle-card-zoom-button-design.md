# Vehicle Card Zoom Button

**Date:** 2026-06-06  
**Status:** Approved

## Summary

Add a zoom-to-vehicle button on each vehicle card in the fleet overview. Clicking it pans and zooms the Leaflet map to that vehicle's location without navigating to the vehicle details page. On mobile, the vehicles panel collapses so the map becomes visible.

## Scope

Single file: `web/src/views/fleet-overview.ts`

No backend changes. No new dependencies.

## Button Placement & Visibility

- Circular icon button (32×32px) using the `my_location` Material Symbol
- Positioned absolutely in the top-right corner of `.vehicle-card`
- Only rendered when `this.markers.has(v.tokenId)` — vehicles with no location data show no button
- The card wrapper remains an `<a>` tag; the button calls `e.preventDefault()` and `e.stopPropagation()` to prevent link navigation

## Map Behavior

- Calls `this.leafletMap.flyTo(marker.getLatLng(), 14)` — smooth animated pan+zoom to level 14
- No marker highlight or selection state
- Repeated clicks re-center gracefully via Leaflet's flyTo

## Mobile Panel Collapse

- New `@state() private panelCollapsed = false` on the component
- When zoom button is tapped and `window.innerWidth < 768`: set `panelCollapsed = true`
- A `collapsed` CSS class transforms `.vehicles-panel` with `translateY(calc(100% - 56px))`, leaving a thin drag-handle strip visible at the bottom
- Tapping the drag handle sets `panelCollapsed = false` (expands)
- On desktop (≥768px): panel is a sidebar, collapse is never triggered

## CSS Changes

- `.vehicle-card` gets `position: relative` (already block-level, minimal impact)
- New `.zoom-btn` styles: absolute position top-right, 32×32px circle, transparent background, subtle hover state
- New `.vehicles-panel.collapsed` transition + transform rule

## Constraints

- Button handler must check `this.leafletMap` is non-null before calling flyTo
- Button handler must check `this.markers.has(v.tokenId)` is truthy (defensive, mirrors render guard)
- `panelCollapsed` reset is not needed on desktop resize — CSS media query governs sidebar layout regardless of state
