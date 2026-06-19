# Parentos — Design Spec
**Date:** 2026-06-18
**Status:** Approved
**Source:** fleet-lite-app fork

---

## Overview

Parentos is a family vehicle tracking app for safety and emergency purposes, forked from fleet-lite-app. The primary user is a parent tracking teen or young-adult drivers. The initial version focuses on live location tracking and post-trip summaries; geofencing, speed alerts, and curfew monitoring are planned for future increments.

---

## Architecture

Parentos is a direct fork of fleet-lite-app with no changes to the technology stack or repository structure.

```
parentos/
  web/    — Lit 3 + TypeScript + Vite + Leaflet + leaflet.markercluster
  api/    — Go + Fiber + PostgreSQL + SQLBoiler
  charts/ — Helm chart (renamed for parentos)
  docs/
  Makefile
```

- Go module: `github.com/DIMO-Network/parentos`
- Dev hostname: `local-parentos.dimo.org:3009`
- Vehicle data source: DIMO Network only (same telemetry + identity API integration)
- Auth: same JWT + WebAuthn model as fleet-lite-app

No new dependencies are introduced on either side of the stack.

---

## Account Model

Unchanged from fleet-lite-app's peer-member tenant model. All terminology is renamed:

| fleet-lite-app | parentos |
|---|---|
| Tenant | Family |
| Tenant switcher | Family switcher |
| Fleet overview | Family overview |
| Member | Family member |
| Invite member | Add family member |
| Vehicle groups | *(removed)* |
| Glovebox / documents | *(removed — v1)* |

Any family member can add/remove vehicles and invite other members. No parent/child permission hierarchy in v1.

---

## Feature Delta from fleet-lite-app

### Removed

- **Fleet groups** — fleet-specific concept with no family equivalent. Group management modals, `VehicleGroupRef` types, and the `import-group-attestations` CLI subcommand and CronJob are all removed.
- **Glovebox** — vehicle document storage removed for v1. Can be reintroduced as "insurance docs" in a future increment.

### Kept (unchanged)

- Live map with Leaflet marker clustering and quick-view overlay
- Vehicle list with search and dense/expanded card toggle
- Trip replay modal (backbone for trip summaries)
- Family member management + email invitations (Postmark)
- Auto-refresh + selective per-vehicle location refresh with countdown timer
- DIMO vehicle onboarding flow
- Account settings

### Added

- **Trip summary on vehicle cards** — last trip stats surfaced on each vehicle card and in the quick-view overlay. "View route" opens the existing trip replay modal unchanged.

---

## Trip Summary Feature

### UI

Each vehicle card and quick-view panel gains a "Last trip" section below the location/status info:

```
Last trip  Jun 17 · 10:42pm
1h 14m · 38.2 mi · Top speed: 67 mph ✓
142 Oak St → 800 N Michigan Ave
                          [View route →]
```

Top speed color coding:
- Green: < 75 mph
- Amber: 75–85 mph
- Red: > 85 mph

If no trips exist for the vehicle, the section is silently omitted. While loading, a skeleton placeholder is shown.

### Backend — new endpoint

`GET /telemetry/last-trip-summary/:tokenId`

1. Exchange tenant JWT for vehicle JWT (same as existing telemetry endpoints)
2. Call DIMO trips API to get the most recent trip window (start/end timestamps)
3. Fetch waypoints for that window (same GraphQL query the trip replay modal uses)
4. Compute server-side: duration, distance (haversine sum over waypoints), top speed
5. Return JSON — no DB storage; responses cached for 45s (matching existing location cache TTL)

Response shape:
```json
{
  "startedAt": "2026-06-17T22:42:00Z",
  "endedAt": "2026-06-17T23:56:00Z",
  "durationSeconds": 4440,
  "distanceMiles": 38.2,
  "topSpeedMph": 67.4,
  "startAddress": "142 Oak St, Chicago, IL",
  "endAddress": "800 N Michigan Ave, Chicago, IL",
  "startLat": 41.8827,
  "startLon": -87.6233,
  "endLat": 41.8936,
  "endLon": -87.6239
}
```

Address labels are reverse-geocoded using OpenStreetMap Nominatim (no API key required). On failure, the field is a formatted coordinate string (`"lat, lon"`). Start/end coordinates are always included in the response for client-side fallback.

### Frontend — integration points

- `vehicle-quick-view.ts` — add "Last trip" section below existing status info
- `fleet-list-view.ts` (renamed `family-list-view.ts`) — add last trip stats inline on list cards
- Trip summary fetches in parallel with location on page load
- "View route" button passes existing trip timestamps to `trip-replay-modal.ts` (no changes to the modal itself)

---

## Visual Redesign

### Color Palette

| Role | fleet-lite-app | parentos |
|---|---|---|
| Primary accent | `#69dbad` (teal) | `#f59e0b` (amber) |
| Safe / online | `#69dbad` | `#4ade80` (green) |
| Alert / warning | `#f5c84b` | `#fb923c` (orange) |
| Emergency / error | — | `#ef4444` (red) |
| Background | `#0f1117` | `#0f1117` |
| Surface | `#1a1d27` | `#1a1d27` |

Dark base is preserved — maps read best on dark.

### Branding

- App name: **Parentos**
- Side nav header: shield + car icon, amber wordmark
- Favicon: shield icon
- Marker cluster style updated to match amber accent

### Vehicle Status Dots

- Green: online / located now
- Amber: last seen > 1 hour ago
- Grey: offline / no location data

### Typography & Copy Tone

- Same system sans-serif font stack (no new font dependency)
- Heading font-weight reduced slightly (less industrial)
- UI copy rewritten from fleet-management tone to family tone throughout (e.g. "your family's vehicles", "add a driver", "family members")

---

## Backend Changes Summary

| Area | Change |
|---|---|
| Go module | `fleet-lite-app` → `parentos` |
| New endpoint | `GET /telemetry/last-trip-summary/:tokenId` |
| Removed | Fleet group controller, service, models, migrations |
| Removed | Glovebox controller and document storage |
| Removed | `import-group-attestations` CLI subcommand + CronJob |
| Kept | All vehicle, telemetry, tenant/member, invitation, auth endpoints |
| Renamed | "tenant" → "family" in API response fields and log messages |

---

## Out of Scope (v1)

The following safety features are designed for future increments and are explicitly excluded from v1:

- Speed alerts (real-time notification when teen exceeds threshold)
- Geofencing (enter/leave zone notifications)
- Curfew / time-based driving alerts
- Glovebox / insurance document storage
- Teen sub-accounts with restricted permissions

---

## Data Flow

```
Parent browser
    └─ family-overview-view (Lit)
         ├─ GET /vehicles           → VehicleService → DIMO identity-api
         ├─ GET /telemetry/locations/:tokenId  → TelemetryService → DIMO telemetry-api
         └─ GET /telemetry/last-trip-summary/:tokenId  → TripSummaryService → DIMO telemetry-api
```

Trip summary calls are made in parallel with location calls on page load. Results are cached server-side at 45s TTL. The frontend renders summaries progressively as each vehicle's response arrives.
