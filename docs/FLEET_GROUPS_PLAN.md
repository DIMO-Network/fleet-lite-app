# Fleet Groups — Implementation Plan (fleet-lite-app)

Port the fleet-group feature + attestation sync from **kaufmann-oracle** into fleet-lite-app.
Per the brief, fleet-lite differs in three ways:

1. **No initial-push backfill CLI** (`resync-group-attestations` is omitted).
2. **Adds the import/sync CronJob** (`import-group-attestations`, read Fetch API → DB).
3. **No group DB tables and no group UI yet** — both are built here, copying kaufmann's
   structure and endpoints, and adding a management UI (incl. a Map-view filter).

Reference implementation: kaufmann-oracle (`docs/adr/0001-fleet-group-attestations.md`,
`internal/controllers/fleet_vehicles.go`, `internal/service/vehicle_groups_attest.go`,
`internal/groupattest/`, `cmd/kaufmann-oracle/import_group_attestations.go`).

---

## What already exists in fleet-lite (reuse — do not rebuild)

| Capability | Location | Notes |
|---|---|---|
| Web framework (Fiber v2) | `api/internal/app/app.go` | `tenantApp` group = JWT + tenant middleware |
| Tenant resolution | `api/internal/app/tenant.go`, `controllers/common.go` `GetWalletAddressFromJWT`, `controllers.GetTenant(c)` | `models.Tenant` in `c.Locals` |
| Multi-tenant creds + AES-256-GCM | `tenants` table (`dimo_client_id`, `dimo_api_key_enc`), `service/tenant.go` `encrypt/decryptSecret`, `GetTenantByID` | decrypted key on `models.Tenant.DIMOPrivateKey` |
| ERC-191 signing | `service/attest_service.go` `signDataSecp256k1` | reuse as-is |
| Attest publish (single CE) | `service/attest_service.go` `submitCloudEvent`, `signedCloudEvent`, `buildParsedCloudEvent` | POSTs to `AttestAPIURL` w/ developer JWT |
| Developer / asset JWT | `gateway/dimo_auth_provider.go` `GetDeveloperJWT`, `GetAssetJWT` | cached |
| Vehicle DID builder | `gateway/dimo_auth_provider.go` `BuildVehicleDID(tokenID uint64)` | `did:erc721:<chain>:<contract>:<tokenId>` |
| Fetch API read | `gateway/fetch_api.go` `ListByDID(tenant, tokenDID, limit) []AttestationEntry` | filters `dimo.document.*`; we'll match `dimo.document.vehicle.groups` |
| CLI framework | `cmd/fleet-lite-app/main.go` (google/subcommands), `migrate.go` | add `import-group-attestations` here |
| DB tooling | goose v3 migrations, sqlboiler v4 models | same as kaufmann |
| Config | `internal/config/settings.go` already has `AttestAPIURL`, `FetchAPIURL`, `ChainID`, `VehicleNftAddress`, `TenantSecretEncKey` | nothing to add |

**Bottom line:** all signing/attest/fetch/tenant-credential plumbing exists. The new work is
the group data model, CRUD endpoints, a per-vehicle group-attestation publisher, the write-path
triggers, the import command + CronJob, and the UI.

## Key differences from kaufmann (drive the design)

- **Vehicle identity is `token_id`, not IMEI.** `vehicles` PK is `(tenant_id, token_id)`; IMEI is
  a nullable column. So the join table keys on `token_id`, routes are naturally
  `/fleet/vehicles/:tokenID/group/:groupID` with **no IMEI↔token resolution step** (simpler than
  kaufmann), and the attestation subject (`BuildVehicleDID(token_id)`) maps directly.
- **No river/job system.** The write-path needs an async strategy — see Decision 1.
- **Bare (non-schema-qualified) table names.** Existing migrations create `tenants`, `vehicles`,
  `tenant_users` without a schema prefix — match that (do **not** use a `fleet_lite_app.` prefix).
- **Per-tenant name uniqueness.** Scope group `name` unique to `(tenant_id, name)` (kaufmann made
  it globally unique, which is wrong for multi-tenant).

## Decisions (locked — 2026-06-09)

1. **Write-path async mechanism:** **goroutine + small bounded retry** (e.g. 3 tries with backoff),
   best-effort, logged, never fails the request — DB is source of truth, attestation is
   eventually-consistent. Group recolor/delete fan-out also runs in a goroutine. **No river.**
   *Known gap (accepted):* the import is foreign-only, so it won't republish our own failed writes;
   the goroutine retry is the only resilience.
2. **Per-group access control:** **not included.** No `access_fleet_groups` table. Any tenant member
   manages all of the tenant's groups (gate on `tenant_users` membership only).
3. **Attestation event type:** `dimo.document.vehicle.groups` — **confirmed**, same type as
   kaufmann-oracle. (`fetch_api.ListByDID` admits it via its `dimo.document.*` prefix filter.)
4. **UI: group rename disabled** (name immutable after creation) to bound re-attest fan-out — same
   as kaufmann/b2b.
5. **Import merge semantics** (revised — diverges from kaufmann): **additive merge**, not full
   mirror. A tenant/org may run several apps (this one, other DIMO apps, third-party) under the
   **same `dimo_client_id`**, so the producer wallet — not the CE source/client id — distinguishes
   one app's view from a sibling's. The import therefore: takes the **latest group attestation per
   producer**, **unions** their groups, **adds** any membership not already present, and **never
   removes** (no producer is authoritative over removals when credentials are shared). De-dup is
   guaranteed by the `(tenant_id, token_id, fleet_group_id)` primary key. Unknown groups are
   auto-created. **No `dimo_client_id` skip** (the old "foreign-only" filter is removed).
6. **Map filter UX:** **single-select** group dropdown that filters both the vehicle list and the
   map pins.

---

## Backend changes

### B1. Migration — `api/internal/db/migrations/<ts>_fleet_groups.sql`
Bare table names (match existing convention). DDL:

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS fleet_groups (
    id         TEXT PRIMARY KEY,                  -- slug(name) per tenant; stable, used as attestation group id
    name       TEXT NOT NULL,
    color      VARCHAR(7) NOT NULL,
    tenant_id  UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS idx_fleet_groups_tenant_id ON fleet_groups (tenant_id);

CREATE TABLE IF NOT EXISTS vehicle_fleet_groups (
    tenant_id      UUID   NOT NULL,
    token_id       BIGINT NOT NULL,
    fleet_group_id TEXT   NOT NULL REFERENCES fleet_groups (id) ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id, fleet_group_id),
    FOREIGN KEY (tenant_id, token_id) REFERENCES vehicles (tenant_id, token_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_vehicle_fleet_groups_group ON vehicle_fleet_groups (fleet_group_id);

-- +goose Down
DROP TABLE IF EXISTS vehicle_fleet_groups;
DROP TABLE IF EXISTS fleet_groups;
```
*(No `access_fleet_groups` — per Decision 2.)* Then regenerate sqlboiler models (`make` target used for the existing models).

### B2. Models
Regenerate sqlboiler → `db/models/fleet_groups.go`, `db/models/vehicle_fleet_groups.go`.

### B3. `service/fleet_group.go` — `FleetGroupService`
CRUD + membership, all tenant-scoped:
- `ListGroups(ctx, tenantID)`, `CreateGroup(ctx, tenantID, name, color)`, `UpdateGroup`, `DeleteGroup`.
- `AddVehicle(ctx, tenantID, tokenID, groupID)`, `RemoveVehicle(...)`.
- `LoadVehicleGroups(ctx, exec, tenantID, tokenID) []GroupRef` (join `vehicle_fleet_groups → fleet_groups`) — shared with the attest publisher and the import command.
- `GroupMemberTokenIDs(ctx, tenantID, groupID) []int64` (for fan-out).
- `slug(name)` helper for the group id.

### B4. Per-vehicle group attestation — extend `attest_service.go`
Add to `AttestService` a method that publishes the per-vehicle membership document (single parsed
CloudEvent, **not** a raw+parsed pair):
```go
// subject = BuildVehicleDID(tokenID); type = "dimo.document.vehicle.groups"
// data    = { "groups": [ {id,name,color}, ... ] }  (empty slice allowed)
AttestVehicleGroups(tenant models.Tenant, tokenID uint64, groups []GroupRef) (eventID string, err error)
```
Reuse `signDataSecp256k1`, `submitCloudEvent`, and the `signedCloudEvent` shape (Source =
`tenant.ClientID`, Subject = `BuildVehicleDID`, Signature over the JSON `data`). Constant
`VehicleGroupsCloudEventType = "dimo.document.vehicle.groups"`.

### B5. Controller + routes — `controllers/fleet_groups.go`, registered in `app.go` under `tenantApp`
```
GET    /fleet/groups
POST   /fleet/groups                         {name,color}
GET    /fleet/groups/:id
PATCH  /fleet/groups/:id                      {name?,color?}
DELETE /fleet/groups/:id
POST   /fleet/vehicles/:tokenID/group/:groupID    (minted-only is implicit: vehicle row exists)
DELETE /fleet/vehicles/:tokenID/group/:groupID
```
Also expose each vehicle's groups for the map/list filter — either add a `groups` field to the
existing `GET /vehicles` response (eager-load `vehicle_fleet_groups`), or add
`GET /fleet/groups/:id/vehicles`. Recommended: add `groups: [{id,name,color}]` to the `/vehicles`
payload so the frontend can filter client-side.

### B6. Write-path triggers (per Decision 1)
- `AddVehicle` / `RemoveVehicle` → republish **that** vehicle's document.
- `UpdateGroup` (recolor; rename disabled in UI) → fan out to all members.
- `DeleteGroup` → capture members **before** delete, then republish each without the group.
- `CreateGroup` (empty) → nothing.
Each: build groups via `LoadVehicleGroups`, call `AttestVehicleGroups`, best-effort.

### B7. Import command — `cmd/fleet-lite-app/import_group_attestations.go`
google/subcommands command `import-group-attestations [-tenant-id] [-token-id] [-dry-run]`,
registered in `main.go`. Logic (mirror kaufmann's, adapted to token_id + `ListByDID`):
- For each tenant (with `dimo_client_id`) → each vehicle (`token_id`):
  - `did = BuildVehicleDID(token_id)`; `entries = fetchAPI.ListByDID(tenant, did, N)`.
  - Pick the **latest** entry with `Type == dimo.document.vehicle.groups`.
  - **Foreign-only:** skip if `entry.Source == tenant.ClientID`.
  - Parse `data.groups`; **full-mirror** reconcile `vehicle_fleet_groups` for `(tenant_id, token_id)`;
    **auto-create** missing `fleet_groups` (unique-name collision → log + skip that membership).
  - `-dry-run` logs would-add/remove/create; `-tenant-id`/`-token-id` scope it.

### B8. Helm — CronJob for the import
Copy kaufmann's `charts/.../templates/cronjobs.yaml` + add a `cronJobs:` section to
`charts/fleet-lite-app/values.yaml`:
```yaml
cronJobs:
  - name: import-group-attestations
    schedule: 0 * * * *            # hourly (start opt-in; validate with -dry-run first)
    command: ["/bin/sh","-c","/fleet-lite-app import-group-attestations; CODE=$?; <linkerd shutdown>; exit $CODE"]
```
Leave it disabled/validated-first (full-mirror is destructive). Note: fleet-lite charts have no
cronjobs template today — add the template too.

---

## Frontend changes (`web/`, Lit + Vite + Leaflet, Material 3 tokens, hash routing)

- **`web/src/types/group.ts`** — `FleetGroup { id; name; color; vehicleCount?; createdAt; updatedAt }`.
- **`web/src/services/fleet-group-service.ts`** — singleton over `ApiService` (auto Bearer +
  Tenant-Id): `list`, `create`, `update`, `delete`, `addVehicle(tokenId, groupId)`,
  `removeVehicle(tokenId, groupId)`.
- **Modals** (model on `web/src/elements/upload-document-modal.ts` patterns — fixed overlay,
  Material 3 tokens, custom events):
  - `web/src/elements/create-fleet-group-modal.ts` (create/edit; rename disabled in edit).
  - `web/src/elements/manage-group-vehicles-modal.ts` (assign/remove vehicles by `tokenId`).
- **Groups management view** `web/src/views/groups-management.ts` — list + open modals; add route
  in `web/src/elements/app-root.ts` (`/:tenantId/groups`) and a nav item in
  `web/src/elements/side-nav.ts`.
- **Map-view filter** in `web/src/views/fleet-overview.ts` (+ `elements/fleet-map.ts`): a group
  dropdown above the "Your cars" panel that filters the vehicle cards and the Leaflet pins; use the
  `groups` field added to `/vehicles`. Vehicles are already keyed by `tokenId`.
- Follow existing conventions: `@state()`, custom events, `msg()` (lit-localize), design tokens.

---

## Phasing (suggested PR breakdown)

1. **Data + CRUD** — migration, models, `FleetGroupService`, group CRUD + assign endpoints, `groups`
   on `/vehicles`. No attestation yet.
2. **Write-path attestation** — `AttestVehicleGroups` + triggers (Decision 1 mechanism).
3. **Import + CronJob** — `import-group-attestations` command + Helm cronjob (kept opt-in).
4. **Frontend** — service, modals, groups view + nav/route, map filter.

## Files to create / edit (checklist)

**Create**
- `api/internal/db/migrations/<ts>_fleet_groups.sql`
- `api/internal/db/models/fleet_groups.go`, `vehicle_fleet_groups.go` (generated)
- `api/internal/service/fleet_group.go`
- `api/internal/controllers/fleet_groups.go`
- `api/cmd/fleet-lite-app/import_group_attestations.go`
- `charts/fleet-lite-app/templates/cronjobs.yaml`
- `web/src/types/group.ts`, `web/src/services/fleet-group-service.ts`
- `web/src/elements/create-fleet-group-modal.ts`, `web/src/elements/manage-group-vehicles-modal.ts`
- `web/src/views/groups-management.ts`

**Edit**
- `api/internal/service/attest_service.go` (add `AttestVehicleGroups`)
- `api/internal/app/app.go` (register group routes)
- `api/internal/controllers/vehicles.go` + vehicle service (add `groups` to list response)
- `api/cmd/fleet-lite-app/main.go` (register `import-group-attestations`)
- `charts/fleet-lite-app/values.yaml` (add `cronJobs:`)
- `web/src/elements/app-root.ts` (route), `web/src/elements/side-nav.ts` (nav)
- `web/src/views/fleet-overview.ts`, `web/src/elements/fleet-map.ts` (group filter)

---

All design decisions are locked (see "Decisions" above). The plan is ready to implement as-is.
