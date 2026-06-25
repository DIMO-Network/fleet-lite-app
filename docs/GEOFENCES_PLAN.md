# Geofences — Implementation Plan (fleet-lite-app)

Add a **Geofences** feature: a tenant defines named polygon geofences (with a speed
limit), assigns them to vehicles (all / by group / manual), draws/edits them on a map,
and CRUDs them from a new left-nav section. Storage leverages **DIMO documents
(attestations)**, mirroring [Glovebox](GLOVEBOX.md) and [Fleet Groups](FLEET_GROUPS_PLAN.md),
with one new twist: **geofence definitions are attested at the tenant / client-id (dev
license) level**, while **manual vehicle↔geofence mappings are attested at the vehicle
level** — same per-vehicle pattern as groups.

Reference implementations to mirror: `service/fleet_group.go`, `service/attest_service.go`
(`AttestVehicleGroups`), `service/group_sync.go`, `gateway/fetch_api.go`,
`controllers/fleet_groups.go`, and on the frontend `views/groups-management.ts`,
`services/fleet-group-service.ts`, the modals, and the Leaflet map in
`views/fleet-overview.ts`.

Created: 2026-06-24
Status: design / pre-implementation. Scope below is **Phase 1 only** (CRUD + map +
assignment). Event detection and alerts are later phases, sketched at the end.

---

## Decisions (locked — 2026-06-24)

1. **Geometry = polygon only.** Stored as a **GeoJSON Polygon**. `area_m2` is computed
   (geodesic) from the polygon and stored/displayed; not user-editable. (Circle/other
   geometries can be added later behind the same GeoJSON column without a migration.)
2. **Phase 1 scope = CRUD + map + assignment.** Define, draw, list, edit, delete
   geofences; assign vehicles. **No** enter/exit, dwell, or speed-exceeded detection yet
   (Phase 2), **no** trips integration or alerts yet (Phase 3).
3. **Assignment is per-geofence, one of three scopes:**
   - **`all`** (default) — applies to every vehicle in the tenant. Scope lives **only**
     in the root definition document; nothing is written per vehicle.
   - **`group`** — applies to vehicles in named fleet-group id(s). Membership is
     **derived** from the existing `vehicle_fleet_groups` table; nothing per vehicle.
   - **`manual`** — applies to an explicit set of vehicles. **This** is the only scope
     that writes a per-vehicle `dimo.document.vehicle.geofences` attestation.
   - *Effective geofences for a vehicle* = union of the three (resolved at read time).
4. **Dual-level attestation storage** (the key design point):
   - **Definitions** → one CloudEvent, **subject = tenant client-id DID**, type
     `dimo.document.fleet.geofences`, data = the full geofence catalog for that tenant.
     Re-published (full list) on any geofence create/update/delete.
   - **Manual mappings** → per-vehicle CloudEvent, **subject = `BuildVehicleDID(tokenID)`**,
     type `dimo.document.vehicle.geofences`, data = `{ "geofences": [id, …] }`. Exactly
     mirrors `AttestVehicleGroups`. Re-published on manual add/remove for that vehicle.
   - Both producer-stamped `GeofenceAttestationProducer = "fleet-lite-app"`.
5. **Local DB is a cache + query/search index** (same role as `fleet_groups`). The map/list
   read from Postgres; attestations are the durable record. (Pull-back sync mirrors
   `GroupSyncService` and is deferred — see Phase 1.5.)
6. **Speed limit stored in km/h** (`speed_limit_kph`, nullable), rendered through the
   existing metric/imperial units toggle. Optional per geofence.
7. **Rename allowed, id is a stable slug** (`slug(name)` per tenant, like groups) so the
   attestation id is stable across renames. *(Groups disabled rename to bound fan-out; here
   the catalog is a single root CE, so a rename is one re-publish — cheap. Revisit if we
   later materialize per-vehicle geofence docs for all/group scopes.)*
8. **Bare (non-schema-qualified) table names** in the migration — match existing
   migrations (`search_path`/DB_NAME resolves the schema; `make sqlboiler` strips the
   prefix). See [[db-schema-from-dbname]].

> **PRIMARY RISK — RESOLVED ✅ (spike 2026-06-24, see Open Questions #1):** verified against
> live DIMO mainnet that the **Attest API accepts a non-vehicle subject** and the **Fetch API
> reads it back**. The tenant-level definition document is viable with **no new auth/fetch
> code** — the existing `GetAssetJWT` + `ListByDIDAndType` work for a tenant DID as-is. The
> only new piece is a one-line `BuildTenantDID`. Locked facts:
> - **Subject DID = `did:ethr:<chainId>:<clientId>`.** A raw `0x…` address is **rejected**
>   (`400 invalid DID`) — the "0x client id" must be DID-wrapped.
> - **Publish** exactly like a vehicle group CE: developer-JWT bearer, data signed with the
>   dev private key (ERC-191), `source = clientId`, `producer = fleet-lite-app`.
> - **Read** with `GetAssetJWT(tenant, tenantDID)` then fetch-api `cloudEvents(did)`. The dev
>   license can self-mint an asset JWT for its own ethr DID. The developer JWT used **directly**
>   as the fetch bearer is rejected (`401`), same as vehicles — exchange is mandatory.
> - **Indexing lag ≈ 7s** confirmed (event appeared on the 2nd poll), consistent with
>   [GROUP_SYNC.md](GROUP_SYNC.md)'s 5–10s window.

---

## What already exists in fleet-lite (reuse — do not rebuild)

| Capability | Location | Notes |
|---|---|---|
| Tenant resolution / membership middleware | `app/app.go` `tenantApp`, `app/tenant.go`, `controllers/common.go` `GetWalletAddressFromJWT` | all geofence routes hang off `tenantApp` |
| ERC-191 signing + CE submit | `service/attest_service.go` `signDataSecp256k1`, `buildParsedCloudEvent`, `submitCloudEvent` | reuse as-is |
| Per-vehicle membership attestation | `service/attest_service.go` `AttestVehicleGroups` (subject = vehicle DID, producer-stamped, single parsed CE) | **clone** for `AttestVehicleGeofences` |
| Developer / asset JWT (cached, exp-aware) | `gateway/dimo_auth_provider.go` `GetDeveloperJWT`, `GetAssetJWT` | dev JWT POSTs attestations |
| Vehicle DID builder | `gateway/dimo_auth_provider.go` `BuildVehicleDID(tokenID)` → `did:erc721:<chain>:<contract>:<tokenId>` | reuse; **add** a tenant-DID builder |
| Fetch read by type | `gateway/fetch_api.go` `ListByDIDAndType(did, type, limit)`, `AttestationEntry` | reuse for pull-back sync (Phase 1.5) |
| Group CRUD + membership service | `service/fleet_group.go` (slug id, tenant-scoping, `GroupMemberTokenIDs`, `VehicleGroupsMap`) | **template** for `GeofenceService`; also the source for group-scope resolution |
| Pull/reconcile sync | `service/group_sync.go` `SyncVehicle`, `import_group_attestations.go` | **template** for geofence pull-back (deferred) |
| Group controller + best-effort republish | `controllers/fleet_groups.go` (`republishVehicles` goroutine + `attestWithRetry` 3×backoff) | **template** for geofence republish |
| Config (attest/fetch/chain/client-id) | `config/settings.go` `AttestAPIURL`, `FetchAPIURL`, `ChainID`, `VehicleNftAddress`, `DimoAuthClientID`, per-tenant `tenant.ClientID`/`tenant.DIMOPrivateKey` | nothing new required for Phase 1 |
| DB tooling | goose v3 migrations, sqlboiler v4 | `make addmigration`, `make migrate`, `make sqlboiler` |
| Frontend group feature | `types/group.ts`, `services/fleet-group-service.ts`, `views/groups-management.ts`, `elements/create-fleet-group-modal.ts`, `elements/manage-group-vehicles-modal.ts` | **template** for geofence equivalents |
| Leaflet map | `views/fleet-overview.ts` (`L.map`, CircleMarkers, trip polylines, theme tile-swap) | reuse map setup; **add** polygon draw + render |
| API client (auto Bearer + Tenant-Id) | `services/api-service.ts` `get/post/patch/delete` | reuse |
| Vehicle carries `groups` | `types/vehicle.ts` `groups: VehicleGroupRef[]` | add an analogous (computed) effective-geofences field if the map needs it |
| Units toggle + localization | `utils/units.ts`, `@lit/localize` `msg()` (render-time thunks) | render speed limit + areas; wrap copy |

**Bottom line:** the per-vehicle (manual) half is a near-verbatim clone of groups. The new
engineering is (a) the **tenant client-id-level definition document** + its DID, (b) **polygon
geometry** (draw tool, GeoJSON column, geodesic area), and (c) **scope resolution** (all /
group / manual) into effective membership.

---

## Storage model (the core design)

```
                 ┌──────────────────────────────────────────────────────────────┐
                 │  Tenant (dev license, client_id = 0x…)                        │
                 │                                                              │
   ROOT CE  ───► │  subject = BuildTenantDID(tenant.ClientID)                   │
 (1 per tenant)  │  type    = dimo.document.fleet.geofences                     │
                 │  producer= fleet-lite-app                                    │
                 │  data    = { geofences: [                                    │
                 │      { id, name, color, geometry:<GeoJSON Polygon>,          │
                 │        areaM2, speedLimitKph, scope:"all"|"group"|"manual",  │
                 │        groupIds?:[…], createdBy, createdAt, updatedAt }, …  ]}│
                 └──────────────────────────────────────────────────────────────┘
                                          │  manual scope only
                                          ▼
                 ┌──────────────────────────────────────────────────────────────┐
 PER-VEHICLE CE  │  subject = BuildVehicleDID(tokenId)                          │
 (manual only)   │  type    = dimo.document.vehicle.geofences                   │
                 │  producer= fleet-lite-app                                    │
                 │  data    = { geofences: [ "<geofenceId>", … ] }              │
                 └──────────────────────────────────────────────────────────────┘

Effective geofences(vehicle) =
      { g : g.scope == "all" }
    ∪ { g : g.scope == "group"  ∧ vehicle ∈ members(g.groupIds) }   ← via vehicle_fleet_groups
    ∪ { g : g.scope == "manual" ∧ geofenceId ∈ vehicle's manual CE } ← via vehicle_geofences
```

Local Postgres mirrors both: `geofences` (the catalog) + `vehicle_geofences` (manual join).

---

## Backend changes (`api/`)

### B1. Migration — `internal/db/migrations/<ts>_geofences.sql`
Bare table names. DDL (sketch):

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS geofences (
    id              TEXT PRIMARY KEY,                 -- slug(name) per tenant; stable attestation id
    tenant_id       UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    color           VARCHAR(7) NOT NULL,
    geometry        JSONB NOT NULL,                   -- GeoJSON Polygon
    area_m2         DOUBLE PRECISION NOT NULL DEFAULT 0,
    speed_limit_kph INTEGER,                          -- nullable
    scope           TEXT NOT NULL DEFAULT 'all',      -- all | group | manual
    group_ids       TEXT[] NOT NULL DEFAULT '{}',     -- when scope = group
    created_by      VARCHAR(43) NOT NULL,             -- wallet of creator
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS idx_geofences_tenant_id ON geofences (tenant_id);

CREATE TABLE IF NOT EXISTS vehicle_geofences (        -- manual scope only
    tenant_id    UUID   NOT NULL,
    token_id     BIGINT NOT NULL,
    geofence_id  TEXT   NOT NULL REFERENCES geofences (id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id, geofence_id),
    FOREIGN KEY (tenant_id, token_id) REFERENCES vehicles (tenant_id, token_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_vehicle_geofences_geofence ON vehicle_geofences (geofence_id);

-- +goose Down
DROP TABLE IF EXISTS vehicle_geofences;
DROP TABLE IF EXISTS geofences;
```
Then `make sqlboiler` → `db/models/geofences.go`, `vehicle_geofences.go`.

### B2. `service/geofence.go` — `GeofenceService` (template: `fleet_group.go`)
- `ListGeofences(ctx, tenantID)` → catalog with vehicle counts (effective).
- `GetGeofence`, `CreateGeofence(name, color, geometry, speedLimitKph, scope, groupIds, createdBy)`,
  `UpdateGeofence`, `DeleteGeofence` — all tenant-scoped; `slug(name)` id; unique-name handling.
- Compute & store `area_m2` from the GeoJSON polygon (geodesic). *(Decide compute side — see
  Open Q #3; leaning: frontend sends polygon, backend computes authoritative area.)*
- `AddVehicle` / `RemoveVehicle(tenantID, tokenID, geofenceID)` — manual membership (idempotent),
  verifies geofence + vehicle belong to tenant; only valid when `scope = manual`.
- `EffectiveTokenIDs(ctx, tenantID, geofence)` — resolve all/group/manual → token-id set
  (group case joins `vehicle_fleet_groups`; reuse `FleetGroupService.GroupMemberTokenIDs`).
- `LoadVehicleGeofences(ctx, exec, tenantID, tokenID)` — effective geofences for one vehicle
  (union of the three scopes) — shared with the attest publisher and `/vehicles` enrichment.

### B3. `gateway/dimo_auth_provider.go` — tenant DID builder (NEW)
- `BuildTenantDID(tenant models.Tenant) string` → `fmt.Sprintf("did:ethr:%d:%s", ChainID, tenant.ClientID)`.
  **Format locked by the spike** (Open Q #1, RESOLVED): ethr DID over the client-id address; a bare
  `0x…` is rejected by the Attest API.
- **Reading** a tenant-subject CE reuses `GetAssetJWT(tenant, tenantDID)` + `FetchAPI.ListByDIDAndType`
  unchanged — no new auth code. (`dimo.document.fleet.geofences` matches the `dimo.document.*`
  prefix `ListByDID` admits, and `ListByDIDAndType` filters it server-side.)

### B4. `service/attest_service.go` — two new publishers
- Constants: `TenantGeofencesCloudEventType = "dimo.document.fleet.geofences"`,
  `VehicleGeofencesCloudEventType = "dimo.document.vehicle.geofences"`,
  `GeofenceAttestationProducer = "fleet-lite-app"`.
- `AttestTenantGeofences(tenant, geofences []GeofenceDef) (eventID string, err error)` — single
  parsed CE, subject = `BuildTenantDID(tenant)`, data = `{ "geofences": [...] }`.
- `AttestVehicleGeofences(tenant, tokenID uint64, geofenceIDs []string) (eventID, err)` — clone of
  `AttestVehicleGroups`, subject = `BuildVehicleDID(tokenID)`, data = `{ "geofences": [id,…] }`.
- Reuse `signDataSecp256k1` / `submitCloudEvent` / developer-JWT POST unchanged.

### B5. `controllers/geofences.go` + routes (template: `fleet_groups.go`), under `tenantApp`
```
GET    /fleet/geofences
POST   /fleet/geofences                          {name,color,geometry,speedLimitKph?,scope,groupIds?}
GET    /fleet/geofences/:id
PATCH  /fleet/geofences/:id                       {name?,color?,geometry?,speedLimitKph?,scope?,groupIds?}
DELETE /fleet/geofences/:id
POST   /fleet/vehicles/:tokenID/geofence/:geofenceID    (manual scope)
DELETE /fleet/vehicles/:tokenID/geofence/:geofenceID
```
- Optionally enrich `GET /vehicles` with effective `geofences: [{id,name,color}]` (like `groups`) so
  the map can color/filter pins client-side. Recommended but can land in the frontend PR.

### B6. Write-path triggers (best-effort goroutine + 3×backoff, mirror `republishVehicles`)
- Any geofence **create/update/delete** → `AttestTenantGeofences` (republish full catalog).
- Manual **add/remove vehicle** → `AttestVehicleGeofences` for that vehicle.
- Changing a geofence's **scope** to/from manual → republish affected vehicles' per-vehicle CE.
- DB is source of truth; attestation is eventually-consistent; never fail the request.

### B7. (Phase 1.5, deferred) Pull-back sync
Mirror `GroupSyncService` + `import-group-attestations`: read the tenant root CE and per-vehicle
CEs back from Fetch API, additively reconcile the local cache (so a sibling app / re-install
converges). Same freshness-gate concerns as groups. Not required to ship the UI.

---

## Frontend changes (`web/`, Lit + Leaflet + Material 3 tokens, hash routing)

- **`types/geofence.ts`** — `Geofence { id; name; color; geometry: GeoJSONPolygon; areaM2;
  speedLimitKph?; scope: 'all'|'group'|'manual'; groupIds?: string[]; createdBy; createdAt;
  updatedAt; vehicleCount? }`.
- **`services/geofence-service.ts`** — singleton over `ApiService`: `list`, `create`, `update`,
  `delete`, `addVehicle(tokenId, geofenceId)`, `removeVehicle(...)`. (Template:
  `fleet-group-service.ts`.)
- **`views/geofences-management.ts`** — new left-nav view: full-bleed **Leaflet map** (reuse the
  `fleet-overview.ts` setup: theme tile-swap, bounds) + a side list of geofences. Draw a polygon to
  create; click a geofence to select/edit; render existing geofences as `L.polygon` layers
  (`new Map<string, L.Polygon>()`, analogous to the vehicle-marker map). Show name, area, speed
  limit, scope, vehicle count per row.
- **`elements/create-geofence-modal.ts`** — name, color (preset swatches), optional speed limit
  (units-aware), **scope selector** (`all` | `group` → multiselect of existing fleet groups |
  `manual`); geometry comes from the map-draw buffer. (Template: `create-fleet-group-modal.ts`.)
- **`elements/manage-geofence-vehicles-modal.ts`** — only when `scope = manual`; toggle vehicle
  membership with live search. (Template: `manage-group-vehicles-modal.ts`.)
- **Polygon drawing** — add a draw capability to Leaflet. Lib TBD (Open Q #2): `leaflet-geoman`
  (community), `leaflet-draw`, or a hand-rolled click-to-add-vertex. Compute live area for display
  with a small geodesic helper (e.g. turf `area`, or a local shoelace-on-sphere).
- **`elements/side-nav.ts`** — add nav key `'geofences'`, icon (`'fence'` / `'pin_drop'`),
  `label: () => msg('Geofences')`, suffix `'/geofences'`.
- **`elements/app-root.ts`** — route `{ path: '/:tenantId/geofences', render: … }`, import the
  view, extend `deriveActive`.
- **Localization** — wrap all copy in `msg()`/`str`; `npm run localize:extract` → translate
  `xliff/es.xlf` → `localize:build`; commit generated bundles. See [LOCALIZATION.md](LOCALIZATION.md).

---

## Phasing (suggested PR breakdown)

1. **Backend data + CRUD** — migration, models, `GeofenceService` (incl. scope resolution + area),
   controller + routes, `BuildTenantDID`. No attestation yet (or stub it).
2. **Dual-level attestation** — `AttestTenantGeofences` + `AttestVehicleGeofences` + write-path
   triggers. **Gated on the Open-Q-#1 spike.**
3. **Frontend** — types, service, geofences view (map + list), create/edit + manage-vehicles
   modals, polygon draw + render, nav/route, localization.
4. **(Phase 1.5, optional)** Pull-back sync (cron + lazy), mirroring groups.

### Phase 1.5 — pull-back sync — **PENDING TODO (deferred, 2026-06-25).** Not building now;
recorded as a pending item. Clone of `GroupSyncService` + `import-group-attestations`: read the
tenant root CE + per-vehicle CEs back from Fetch API and additively reconcile the local cache so a
sibling app / re-install converges. No user-facing payoff until there's a second consumer — revisit
then.

### Phase 2 — event detection — **PLANNED (decisions locked 2026-06-25). On-demand telemetry compute, NO cron.**

Mechanism = **backend telemetry computation** (not webhooks), computed **just-in-time** on view, with
**summary results cached in the DB** (past trips/fixes are immutable → a computed result never changes).
Locked decisions (2026-06-25): **speed included from the start**; **both entry points**; entry-point-2
fan-out is **capped + bounded-concurrency + progressive/streaming results** (not restricted to assigned).

**Data source — verified (telemetry_api.go `TripReplay`).** telemetry-api's `signals` query is
interval-bucketed and multi-signal, so one call returns coordinates **and** speed:
```graphql
route: signals(tokenId: %d, from: %q, to: %q, interval: "30s") {
  timestamp
  currentLocationCoordinates(agg: LAST) { latitude longitude }
  speed(agg: MAX)
}
```
→ a new `TelemetryAPIService.GeofenceSamples(tenant, tokenID, from, to) []GeoSample{ts, lat, lng, speedKph}`.
Speed is MAX-per-bucket, so speed-exceeded resolves to the bucket granularity (30s default; tighten if
needed). Per-vehicle asset JWT required (developer JWT rejected) — reuse the per-vehicle exchange +
`permissionsRequired` skip already used by `FleetLocations`/`Latest`.

**Two entry points, one compute path:**
1. **Trip panel** — clicking a trip computes (or reads cache for) which geofences that trip's route
   crossed: enter/exit timestamps, dwell, max speed + exceeded. Cheap: one vehicle, one trip window,
   the same route data replay already fetches.
2. **Geofence window query** — clicking a geofence + a window (**≤ 3 days**, enforced server-side)
   computes which effective vehicles passed through it, same per-pass calcs. Fan-out over effective
   vehicles with **bounded concurrency** (reuse `FleetLocations`' worker pool), **skip
   no-permission vehicles**, **stream results progressively** (per-vehicle as each completes) with an
   "N of M vehicles" progress signal. Cap the vehicle count (config) and `log()` if truncated.

**Cache atom = a "pass"** (contiguous inside-polygon interval), summary form, NOT raw telemetry:
```sql
geofence_passes(
  geofence_id TEXT, token_id BIGINT, tenant_id UUID,
  entered_at TIMESTAMPTZ, exited_at TIMESTAMPTZ, dwell_s INTEGER,
  max_speed_kph DOUBLE PRECISION, entry_lat/lng, exit_lat/lng,
  max_speed_lat/lng,                       -- coords of the speeding bucket
  num_samples INTEGER,
  PRIMARY KEY (geofence_id, token_id, entered_at))
geofence_scan_coverage(                     -- which ranges are already computed
  geofence_id TEXT, token_id BIGINT, tenant_id UUID,
  scanned_from TIMESTAMPTZ, scanned_to TIMESTAMPTZ)
```
Both entry points = "passes overlapping interval X". Before computing, subtract covered ranges from the
requested window and only fetch the **gaps**; then merge coverage. A pass fully in the past + inside
covered range never recomputes. Geometry is **immutable per geofence id** (edit = delete+redraw), so
cached passes stay valid. `speed_limit_kph` **can** change on edit → store raw `max_speed_kph`, evaluate
"exceeded?" against the *current* limit at **read** time (never bake the verdict into the cache).

**Pass-detection algorithm** (`service/geofence_detection.go`): fetch `GeoSample`s for the gap window →
ray-cast point-in-polygon (new `geo` helper, outer ring) over the ordered samples → each maximal run of
inside-samples = one pass; `entered_at`/`exited_at` from the run's edge timestamps (optionally midpoint
to the prior/next outside sample), `dwell_s` = exited−entered, `max_speed_kph` + its coords = max over
the run. Sampling-interval error is inherent (a vehicle can clip a small fence between 30s buckets) —
documented limitation; tighten interval per-query if a fence is small.

**Endpoints (under `tenantApp`):**
```
GET /telemetry/:tokenID/trip-geofences?from&to   → entry 1: passes for this trip vs all tenant geofences
GET /fleet/geofences/:id/passes?from&to           → entry 2: passes in window; streams (SSE/chunked) or
                                                     client-paginates token-id batches like the map does
```
**Frontend:** trip-details panel section (geofences this trip crossed, with timing/speed badges);
geofence-view detail with a duration picker (≤3d) + a progressively-filling vehicle/pass list. Reuse
units toggle + `msg()` localization.

**Suggested build order:** (A) samples fetch + `geo` point-in-polygon + passes/coverage cache + entry 1
(single-vehicle, proves the machinery at ~zero scale risk); (B) entry 2 progressive fan-out reusing the
same compute path. Ship as two PRs.

### Phase 3 — trips + alerts (still later)
- Surface geofence info inside trips (enter/exit/dwell overlaid on a trip) **and** a dedicated geofence
  section (builds directly on Phase 2's passes).
- Generic Alerts surface with **geofence-specific alert config** — alerting/notification mechanism
  (webhooks vs in-app) to be decided at Phase 3 kickoff.

---

## Files to create / edit (Phase 1 checklist)

**Create**
- `api/internal/db/migrations/<ts>_geofences.sql`
- `api/internal/db/models/geofences.go`, `vehicle_geofences.go` (generated)
- `api/internal/service/geofence.go`
- `api/internal/controllers/geofences.go`
- `web/src/types/geofence.ts`, `web/src/services/geofence-service.ts`
- `web/src/views/geofences-management.ts`
- `web/src/elements/create-geofence-modal.ts`, `web/src/elements/manage-geofence-vehicles-modal.ts`

**Edit**
- `api/internal/service/attest_service.go` (two publishers + constants)
- `api/internal/gateway/dimo_auth_provider.go` (`BuildTenantDID`)
- `api/internal/app/app.go` (register geofence routes)
- `api/internal/controllers/vehicles.go` + vehicle service (optional: effective `geofences` on `/vehicles`)
- `web/src/elements/side-nav.ts` (nav), `web/src/elements/app-root.ts` (route + deriveActive)
- web localization bundles

---

## Open questions / to verify

1. **Tenant/client-id attestation — RESOLVED ✅ (spike 2026-06-24, live mainnet).** Attest API
   accepts a non-vehicle subject; Fetch API reads it back. **Subject = `did:ethr:<chainId>:<clientId>`**
   (raw `0x…` → `400 invalid DID`). **Read** = `GetAssetJWT(tenantDID)` then `cloudEvents(did)`; the
   dev license self-mints the asset JWT for its own ethr DID. Developer JWT as the fetch bearer →
   `401` (exchange mandatory). Lag ≈ 7s. No new auth/fetch code; only `BuildTenantDID` is added.
   The local-DB-only fallback is **no longer needed**.
2. **Polygon draw library.** `leaflet-geoman` vs `leaflet-draw` vs hand-rolled. Weigh bundle size,
   maintenance, and edit-existing-shape support.
3. **Where is `area_m2` computed?** Frontend (turf/shoelace, for live display) and/or backend
   (authoritative stored value). Leaning: frontend shows live area; backend recomputes on save so the
   stored value is trusted.
4. **Materialize per-vehicle CEs for all/group scopes?** Phase 1 keeps effective membership *computed*
   (zero fan-out for all/group). If Phase 2 detection wants a self-contained per-vehicle geofence
   document, revisit — that reintroduces groups-style fan-out and would argue for disabling rename.
5. **Detection mechanism (Phase 2).** Webhooks vs backend telemetry computation — deferred per Decision.
6. **Group-scope coupling.** When a geofence is `scope = group` and that group's membership changes,
   effective geofences shift automatically (good), but no per-vehicle CE is republished. Confirm that's
   acceptable for downstream (Phase 2) consumers, or materialize then.
