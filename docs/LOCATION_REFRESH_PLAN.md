# Location Refresh & Real-Time Toggle — Implementation Plan

Stop the fleet-overview map from re-pulling every vehicle's location from
telemetry-api on every load. Introduce a **per-vehicle freshness window** (don't
re-pull a vehicle fetched within the last **5 minutes**), do **partial refresh**
(only fan out for stale vehicles, render the rest from the DB cache), and add a
**real-time toggle** for a selected vehicle that polls every **20 seconds**.

Builds directly on the last-seen cache (PR #61): `vehicles.last_lat/last_lon/
last_seen` + the telemetry write-through already exist.

Created: 2026-06-30
Status: design / pre-implementation.

---

## Problem

The map paints markers instantly from the DB cache (PR #61), but
`loadVehicleData` still kicks off the **full per-vehicle telemetry fan-out** on
every fresh load. That's redundant when the cached coordinates are seconds or a
couple minutes old — a hard reload, a new tab, or a new session re-pulls the
entire fleet from telemetry-api for no benefit.

## What already gates re-fetching (reuse — don't rebuild)

| Layer | Where | Behavior |
|---|---|---|
| Frontend `FleetCache` | `web/src/services/fleet-cache.ts` | In-memory singleton, **no TTL**. `loadVehicleData` returns early from it, so SPA navigation (details ↔ map) doesn't re-pull. **Lost on hard reload / new session.** |
| Backend per-vehicle cache | `service/telemetry_api.go` `fleetLocationsCacheTTL = 45s` | Server-side, **shared across a tenant's users**. Repeat calls within 45s skip telemetry-api. |
| DB location cache | `vehicles.last_lat/last_lon/last_seen` (PR #61) | Persisted coords + fix time; survives reloads. Written through after each fan-out. |

The gap the 5-minute window fills: **across hard reloads / new sessions**, where
`FleetCache` is gone and we re-fan-out despite the DB cache being fresh.

## Two timestamps — do not conflate

- **`last_seen`** — the GPS *fix* time (vehicle clock). Display only. A parked
  car's is hours old even if we pulled it 10s ago. (Exists.)
- **`location_pulled_at`** — when *we* last fetched this vehicle from
  telemetry-api. The freshness window keys on **this**. (New.)

---

## Decisions (locked — 2026-06-30)

1. **Freshness store = backend per-vehicle column** `location_pulled_at`
   (Option B), returned on `/vehicles`. Shared across all users/devices of a
   tenant (user A's pull spares user B), and per-vehicle granularity drives both
   partial refresh and the real-time case.
2. **Partial refresh.** On load the frontend fans out **only** for vehicles whose
   `location_pulled_at` is missing or older than the window; the rest render from
   the DB cache. A fully-fresh fleet makes **zero** telemetry calls.
3. **Cadence hardcoded:** freshness window = **5 min**, real-time interval =
   **20 s**. Constants, not config (revisit later if needed).
4. **`location_pulled_at` is stamped on a *real* telemetry fetch only** — never
   on a cache-serve — otherwise the window never expires (see Backend B2).
5. **Real-time toggle is per-selected-vehicle**, off by default, polls only that
   one vehicle with `force=true`, and shows its cadence in the label.

---

## Backend changes (`api/`)

### B1. Migration — `<ts>_add_location_pulled_at_to_vehicles.sql`
Bare table name (see [[db-schema-from-dbname]]). One nullable column; **no
index** (read per-row with the vehicle list, never filtered/sorted on):
```sql
ALTER TABLE IF EXISTS vehicles
    ADD COLUMN IF NOT EXISTS location_pulled_at TIMESTAMPTZ;
```
Then `make sqlboiler`.

### B2. Stamp `location_pulled_at` only on real fetches
PR #61 write-throughs in the **controller** on *every* `FleetLocations` return —
including 45s-cache hits. `location_pulled_at` must reflect an actual
telemetry-api fetch, so the controller needs to know **which vehicles were
fetched fresh** this call.
- Add to `FleetLocationsResult` a `Fetched []uint64` (token IDs the fan-out
  actually queried this call; cache hits are excluded).
- Worker appends `id` to `Fetched` only on a successful live query (the branch
  that builds `LocationCoords` from a fresh response — not the cache-read loop).
- `VehicleService.UpsertLastLocations` (or a sibling) stamps
  `location_pulled_at = now()` for `Fetched`, alongside the existing
  `last_lat/last_lon/last_seen` for all of `Locations`.

### B3. `/vehicles` returns `locationPulledAt`
- `models.Vehicle` gains `LocationPulledAt *time.Time \`json:"locationPulledAt,omitempty"\``.
- `applyLastLocation` (vehicle.go) copies it from the row (same pattern as the
  PR #61 fields).

### B4. (Optional) Backend freshness gate — belt-and-suspenders
The frontend already filters to stale vehicles, so the backend can trust the
request. Optionally, the fan-out can also skip vehicles whose DB
`location_pulled_at` is within the window unless `force`, making the DB the
authoritative cache and letting us **retire the 45s in-memory cache** (two
overlapping caches otherwise). *Leaning: keep it simple for v1 — frontend owns
the gate, keep the 45s in-memory cache as-is, defer this.*

### Subscription / no-permission note
Vehicles in `NoPermissions` have no `location_pulled_at` written, so the frontend
will re-attempt them every window (≈ every 5 min). That's the desired
"periodically re-check access" behavior; the backend 45s cache still suppresses
hammering within a load.

---

## Frontend changes (`web/`)

### F1. Types — `types/vehicle.ts`
`Vehicle` gains `locationPulledAt?: string` (ISO).

### F2. Partial-refresh load — `views/fleet-overview.ts` `loadVehicleData`
- Constants: `LOCATION_FRESH_WINDOW_MS = 5 * 60 * 1000`.
- After seeding markers from the DB cache (PR #61), **partition** the fleet:
  - **fresh** = `locationPulledAt` present and `now - locationPulledAt <
    WINDOW` → already painted from the seed, no call.
  - **stale** = missing or older → include its tokenId in the fan-out.
- Page/fan-out only the **stale** token IDs (reuse the existing chunked
  `tokenIds=` paging; the backend already intersects with the tenant's fleet).
- All-fresh fleet ⇒ skip the fan-out entirely.
- `FleetCache` and the manual **refresh** button keep working; refresh forces a
  full fan-out (`force=true`, ignores the window).

### F3. Real-time toggle (selected vehicle)
- Constant: `REALTIME_INTERVAL_MS = 20 * 1000`.
- Shown **only when a vehicle is selected** (the `vehicle-quick-view` surface).
  Off by default. Label shows cadence, e.g. **"Real-time · 20s"**.
- On enable: `setInterval(20s)` → `TelemetryService.fleetLocations(true,
  [selectedTokenId])` (`force=true` bypasses **both** the frontend window and the
  backend 45s cache — required, or the 20s poll returns stale data 2 of 3 times).
  Update that marker + its `last_seen` in place; the rest of the fleet stays
  static.
- Clear the interval on toggle-off, deselect, view disconnect, or tenant switch.
- If a poll returns the vehicle in `noPermissions`, disable the toggle and show a
  "no location access" note instead of polling a dead vehicle.

### F4. Localization
Wrap new copy ("Real-time", the cadence label) in `msg()`; `localize:extract` →
translate `xliff/es.xlf` → `localize:build`. See [LOCALIZATION.md](LOCALIZATION.md).

---

## Edge cases

- **Clock skew** between server `location_pulled_at` and client `now`: harmless at
  a 5-min window; compare client-side, accept ± a few seconds.
- **Never-pulled vehicles** (`location_pulled_at` NULL): always stale → fetched on
  first load, as today.
- **Real-time + manual refresh** simultaneously: the per-vehicle poll and a full
  refresh both just write through; last-writer-wins on the marker, no corruption.
- **Selected vehicle goes stale while real-time is OFF**: it refreshes on the next
  normal load like any other vehicle (no special-casing).

---

## Phasing / files

1. **Backend** — migration + `location_pulled_at`; `FleetLocationsResult.Fetched`;
   stamp on real fetch; expose on `/vehicles`. (`migrations/…`, `telemetry_api.go`,
   `vehicle.go`, `models.go`, `controllers/telemetry.go`.)
2. **Frontend** — `locationPulledAt` type; partition + partial fan-out in
   `loadVehicleData`; constants. (`types/vehicle.ts`, `views/fleet-overview.ts`.)
3. **Real-time toggle** — UI + 20s force-poll for the selected vehicle; localize.
   (`views/fleet-overview.ts` / `vehicle-quick-view.ts`, `xliff/es.xlf`.)

Ships as 1–2 PRs (backend + frontend window together; the toggle can follow).

## Open questions

1. **Retire the 45s in-memory cache (B4)?** Once the DB window is authoritative
   it's largely redundant; keeping both is harmless but two caches. Defer.
2. **Real-time scope:** only the selected vehicle (planned), or also its visible
   neighbors? Single-vehicle keeps telemetry load trivial; revisit if users want
   a live cluster.
3. **Window vs. backend cache alignment:** if B4 is skipped, the 45s cache and the
   5-min window coexist fine, but a force refresh < 5 min after a normal pull will
   still re-fetch — intended (force = user explicitly wants fresh).
