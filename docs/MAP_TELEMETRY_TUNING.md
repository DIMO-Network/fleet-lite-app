# Map View Telemetry Tuning — Ordered Fixes

> Goal: make the Fleet Overview map's location loading scale past ~100 vehicles,
> and add text search to the vehicle list/map.

Created: 2026-06-10
Status: fixes 1–3 + 6 implemented (perf/map-telemetry-tuning); 4 deferred, 5 external

---

## Problem

The map view loads each vehicle's latest location from telemetry-api via
`GET /telemetry/locations`. The frontend makes **one** request (good — session
cache + manual refresh, no browser N+1), but the backend
(`api/internal/service/telemetry_api.go` `FleetLocations`) processes the fleet
in a **strictly sequential loop**, and each iteration is two network round-trips:

1. `GetVehicleJWT` — token-exchange call per vehicle (cached, fixed 10-min TTL,
   so the whole fleet re-exchanges every ~10 min)
2. One GraphQL `signalsLatest` query to telemetry-api per vehicle

No concurrency, no result caching, no paging. The interface docstring claims a
"single aliased GraphQL request" but the implementation diverged: telemetry-api
rejects the developer JWT, so each query must carry that vehicle's own JWT —
one HTTP request per vehicle is structurally forced by per-vehicle auth.

**Blast radius:** at ~300–500 ms/vehicle sequential:
- 50 vehicles ≈ 15–25 s map load
- ~120–200 vehicles ≈ 60 s+ → nginx ingress 504; the frontend swallows the
  failure and the map silently stays at the default view. Beyond that fleet
  size, locations **never** load.

## Ordered fixes

### 1. Parallelize the fan-out with bounded concurrency ← *start here*
`FleetLocations`: run the per-vehicle work (JWT exchange + query) through
`errgroup.WithContext` with a concurrency limit (~10). Collect results under a
mutex (or a channel). Keeps the per-vehicle auth constraint; removes the serial
cost. 200 vehicles: ~60 s → ~4–6 s.
- File: `api/internal/service/telemetry_api.go`
- No API or frontend changes. Biggest win, smallest diff.
- Also fix the stale interface docstring while in there.

### 2. Short-TTL tenant-level result cache
Cache `tenantID → FleetLocationsResult` for 30–60 s (e.g. `patrickmn/go-cache`,
already a dependency). Repeat map loads and multiple users on the same tenant
stop re-running the whole fan-out; telemetry-api pressure drops.
- Files: `telemetry_api.go` or the controller layer.
- Manual refresh ignores the cache via a `?force=true` param (frontend's
  refresh button passes it; keep default cached).

### 3. JWT cache TTL from token `exp`
`gateway/dimo_auth_provider.go` caches vehicle JWTs for a fixed 10 min. Parse
the JWT's actual `exp` claim and cache until shortly before expiry
(e.g. `exp - 1 min`). Halves the steady-state per-vehicle cost.

### 4. Progressive loading — implemented as client-side paging
Even with the parallel fan-out, one monolithic call means the map paints
nothing until the slowest vehicle resolves. Implemented as client paging:
- Backend: `GET /telemetry/locations?tokenIds=1,2,3` restricts the call to a
  subset (intersected with the tenant's vehicles — no cross-tenant probing).
  The result cache became **per-vehicle** (`tenantID:tokenID` →
  coords/noPerm/no-data outcome, 45 s) so subset and full-fleet requests
  compose instead of keeping separate snapshots.
- Frontend (`fleet-overview.ts`): pages the fleet in chunks of 20 with 3
  requests in flight (sized against the backend's 10-worker pool), adding
  markers per batch (`addMarkers` is additive; full `placeMarkers` redraw only
  on filter changes). Map framed on the first batch with data, re-fit at the
  end. A load-generation counter discards stale chunk results after a tenant
  switch or manual refresh. Old backend + new frontend degrades gracefully
  (each chunk returns the full fleet; frontend dedupes).

### 5. Upstream structural fix (telemetry team ask)
The 2N-requests shape is forced by per-vehicle JWT auth. Ask the telemetry-api
team for a fleet-scoped query authorized by a developer-license JWT (or a batch
token exchange). That collapses 2N → ~2 and obsoletes most of the above.

### 6. Text search (frontend-only v1)
The map/list currently has only a group-filter dropdown. Add a text input that
filters cards **and** map markers on title (year/make/model), token ID,
IMEI/serial — wired into the same `visibleTokenIds` mechanism the group filter
uses. All data is already client-side; instant, no backend change.
Server-side `ILIKE` search (`make/model/year/imei/serial/token_id`) only
becomes necessary if/when server paging is introduced.

## Sequencing

| Order | Fix | Effort | Status |
|---|---|---|---|
| 1 | Bounded-concurrency fan-out | ~1 h | **done** |
| 2 | Tenant result cache (45 s + `?force=true`) | ~1 h | **done** |
| 3 | JWT cache TTL from `exp` (vehicle/asset + developer) | ~30 m | **done** |
| 6 | Frontend text search (list + markers, localized) | ~1–2 h | **done** |
| 4 | Progressive loading (client paging, chunk 20 × 3 in flight) | ~2 h | **done** |
| 5 | Upstream batch query | external | raise with telemetry team |

(1–3 are one backend PR candidate; 6 is a frontend PR; 4–5 deferred.)
