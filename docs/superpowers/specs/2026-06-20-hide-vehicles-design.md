# Hide Vehicles — Design Spec

**Date:** 2026-06-20  
**Status:** Approved

## Problem

Users want to declutter the vehicle list and map by hiding specific vehicles (e.g. inactive, decommissioned, or personally irrelevant vehicles). Hidden vehicles should disappear from all views by default, persist across sessions, and be revealable on demand.

## Scope

- Vehicle panel in map view (`fleet-overview.ts`)
- Vehicle table in list view (`fleet-list-view.ts`)
- Map markers in fleet overview

Out of scope: vehicle detail pages, glovebox, groups management, server-side persistence.

---

## Architecture

### New service: `web/src/services/hidden-vehicles-service.ts`

Singleton following the `PrefsService` pattern (localStorage + window events).

**Storage:** `fleet-lite:hidden:{tenantId}` → JSON array of tokenId strings  
**Event:** `fleet-lite-hidden-changed` dispatched on window after any change

```ts
class HiddenVehiclesService {
  hide(tenantId: string, tokenId: string): void
  unhide(tenantId: string, tokenId: string): void
  isHidden(tenantId: string, tokenId: string): boolean
  getHidden(tenantId: string): Set<string>
  subscribe(cb: () => void): () => void  // returns unsubscribe fn
}
```

Hidden state is per-tenant so switching tenants loads that tenant's own hidden set. Hiding in tenant A does not affect tenant B.

`VehicleCard` type is unchanged — hidden is a UI preference, not a data property.

---

## View changes

### Fleet overview (map view)

**Filtering:**
- `visibleCards()` filters out hidden tokenIds unless `showHidden` local state is active.
- `placeMarkers()` / `addMarkers()` also omit hidden vehicles from map markers.
- When `showHidden` is toggled off, hidden markers are removed; when toggled on, they are added back.

**Hiding a vehicle:**
- A `visibility_off` icon button appears on each card on hover (positioned near the existing zoom button).
- Clicking it calls `hiddenVehiclesService.hide(tenantId, tokenId)`, which removes the card from the list immediately.
- The map marker for that vehicle is removed.

**Showing/unhiding hidden vehicles:**
- Panel header gets a "Show hidden" button (beside the existing search/density/groups buttons).
- When any vehicles are hidden, the button shows a count badge: `visibility_off (3)`.
- When `showHidden` is active, hidden cards appear at the bottom of the list, visually dimmed (50% opacity), with a `visibility` icon to unhide.
- Unhiding a vehicle calls `hiddenVehiclesService.unhide()` and removes the dimmed card immediately.

### Fleet list view (table)

**Filtering:**
- `visibleCards()` filters out hidden tokenIds unless `showHidden` local state is active.

**Controls bar:**
- Same "Show hidden (N)" toggle button added to the existing controls bar.
- When `showHidden` is active, hidden rows appear with reduced opacity and a `visibility` icon button in the action column to unhide.

**Hiding from list view:**
- Hidden state is managed from the map view primarily, but users can also unhide from the list view when the toggle is on. Adding a hide action to the list view rows is also included for symmetry (hover reveals `visibility_off` in the action column).

---

## State lifecycle

| Event | Effect |
|-------|--------|
| User hides vehicle | localStorage updated, window event fired, both views re-filter |
| User unhides vehicle | localStorage updated, window event fired, both views re-filter |
| Page refresh | Hidden set reloaded from localStorage — hides survive refresh |
| Tenant switch | Hidden set reloaded for new tenant |
| FleetCache invalidated | No effect on hidden set |

---

## Files to create / modify

| File | Change |
|------|--------|
| `web/src/services/hidden-vehicles-service.ts` | **Create** — new singleton service |
| `web/src/views/fleet-overview.ts` | Add hide button on cards, "Show hidden" toggle in panel header, filter hidden vehicles from `visibleCards()` and markers |
| `web/src/views/fleet-list-view.ts` | Add hide/unhide action column, "Show hidden" toggle in controls bar, filter hidden vehicles from `visibleCards()` |

---

## UX details

- **Hide trigger:** `visibility_off` icon on card/row hover — doesn't require a separate confirmation step.
- **Reveal toggle:** Single button in panel/table header. Label: `visibility_off` icon + count when any hidden; pressing it enters "show hidden" mode and the button highlights.
- **Hidden vehicle appearance in reveal mode:** 50% opacity, `visibility` icon to restore. Hidden vehicles appear below the visible set in the list; in the map they appear as dimmed markers.
- **Zero hidden state:** The "Show hidden" button is hidden (not rendered) when the hidden set is empty — no visual clutter until there's something to show.
