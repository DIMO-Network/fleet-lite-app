# Collapsible Vehicles Panel (Desktop Sidebar)

**Date:** 2026-06-06  
**Status:** Approved

## Summary

Make the right-side vehicles panel on desktop collapsible. Collapsed state shows a 72px-wide icon strip with per-vehicle zoom and details-navigation actions. Expanded state is the existing full 384px card layout. A chevron tab on the left edge of the panel toggles between states.

## Scope

Single file: `web/src/views/fleet-overview.ts`  
Desktop only (≥768px). Mobile bottom-sheet behavior (`panelCollapsed`) is unchanged.

## State

New `@state() private panelExpanded = true` on `FleetOverviewView`. Default: expanded. This is independent of the existing `panelCollapsed` state (mobile bottom-sheet).

## Toggle Button

- Absolutely positioned tab on the left edge of the panel, vertically aligned with the panel header
- Always visible regardless of expanded/collapsed state
- Icon: `chevron_right` when expanded (click → collapse), `chevron_left` when collapsed (click → expand)
- Styled as a small rounded button that slightly overhangs the panel's left border

## Panel Width Transition

- Expanded: `384px` (existing)
- Collapsed: `72px`
- Transition: `width 0.3s ease` on `.vehicles-panel`
- `overflow: hidden` (already present) contains content during animation

## Panel Header in Collapsed State

When `!panelExpanded`, the header hides the "Your cars" `<h3>` and the `+` button. Only the chevron tab remains. The drag handle (mobile only, hidden at ≥768px anyway) is unaffected.

## Compact Row Layout

When `!panelExpanded`, `renderCard` renders a compact row instead of the full card:

- Element: `<a>` link to vehicle details (same href)
- Height: 72px, full panel width
- Content: 64×64 car icon centered horizontally in the row
- Zoom button (`my_location`): pinned bottom-right corner of the row, only rendered when `this.markers.has(v.tokenId)`
- Zoom button retains `stopPropagation` to prevent link navigation when tapped
- Vehicles without a marker show the row + icon only, no zoom button
- No title, location, seenAt, online badge, or no-permissions badge in this view

## CSS Additions

- `.vehicles-panel`: add `transition: width 0.3s ease` (alongside existing `transition: transform`)
- `.chevron-tab`: absolute, left-edge tab button style
- `.vehicle-card-compact`: compact row styles (height, centering, relative position for zoom button)
- `.vehicles-panel.collapsed .panel-header h3`, `.vehicles-panel.collapsed .panel-header .add-btn`: `display: none`
