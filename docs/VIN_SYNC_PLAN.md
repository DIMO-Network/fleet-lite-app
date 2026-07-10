# VIN Sync via telemetry-api VIN VC — Plan (fleet-lite-app)

Fill `vehicles.vin` for the whole fleet from the DIMO **VIN verifiable
credential**, read directly off telemetry-api — instead of hoping a
registration document gets uploaded. VIN is immutable, so this is a
**pull-once** design: the only gate is `vin IS NULL` — once set, a vehicle
never costs another query.

Created: 2026-07-09
Status: IMPLEMENTED (same day) — `VINFromVC` on the telemetry service, VC
fallback in `LicensePlateSyncService.SyncVehicle`, `-vin-only` backfill flag.
Live-verified on mainnet (token 191347): VC read → cached VIN, re-run skips
the filled vehicle (`checked: 0`). Open question resolved in practice: the
code handles both null-VC and GraphQL-error shapes (both → skip + retry).

---

## Problem

`vehicles.vin` is populated only by `LicensePlateSyncService`
(`api/internal/service/license_plate_sync.go`), which extracts a `vin` field
from `dimo.document.vehicle.registration` attestations — i.e. a VIN exists only
when someone uploaded a registration document to the glovebox for that vehicle.
Most vehicles have no such document, so the column stays NULL and the UI's VIN
row / list column / search-by-VIN mostly come up empty. Telemetry-api is never
consulted today.

## The read path (confirmed 2026-07-09)

Telemetry-api exposes the VIN directly on the VC type — no rawVC parsing:

```graphql
vinVCLatest(tokenId: 190322) {
    vin
}
```

- Auth: per-vehicle JWT with **privilege 5** (`privilege:GetVINCredential`).
  Our `GetVehicleJWT` already exchanges for `[1, 3, 4, 5]`
  (`gateway/dimo_auth_provider.go:101`) — **no permission changes needed**.
- **Almost all fleet vehicles already have a VIN VC** (confirmed by James), so
  no generation step is needed. The rare no-VC vehicle simply stays NULL and is
  re-checked on future passes (see Edge cases; attestation-api generation is a
  deferred fallback we don't expect to need).
- Cost: telemetry queries bill DCX per query. The `vin IS NULL` gate bounds
  total cost to a one-time backfill (~fleet size) plus one read per
  newly-added vehicle.

## What already exists (reuse — do not rebuild)

| Capability | Location | Notes |
|---|---|---|
| Per-vehicle registration-fields sync | `service/license_plate_sync.go` `SyncVehicle` | **extend** — already runs per vehicle in the cron and after a glovebox attest (`controllers/documents.go:182`) |
| Vehicle JWT w/ priv 5 (cached, exp-aware) | `gateway/dimo_auth_provider.go` `GetVehicleJWT` | reuse as-is |
| Telemetry GraphQL client (per-vehicle JWT) | `service/telemetry_api.go` `doQuery` | add one small query method |
| Cron driver (daily warm / weekly full) | `cmd/fleet-lite-app/import_group_attestations.go` (plate sync at :146) | VIN step slots into the same per-vehicle loop |
| No-permission skip semantics | `FleetLocations` noPerm handling | JWT-exchange failure → skip quietly, never fatal |
| `vehicles.vin` column + `/vehicles` exposure + UI | migration `20260629120000`, `service/vehicle.go:138,167`, list/card/search | frontend needs **zero** changes |

## Decisions

1. **Read from telemetry-api only** — `vinVCLatest { vin }` through the
   existing GQL gateway. No attestation-api client, no new config value.
2. **Fill-if-missing only.** Never overwrite a non-NULL `vin`, whichever source
   set it (registration doc or VC). Avoids churn and any precedence question.
3. **Placement: extend `LicensePlateSyncService.SyncVehicle`** (it is already
   the "registration fields" per-vehicle pass) — constructor gains the
   telemetry service.
4. **Skip quietly** on JWT-exchange failure (no SACD) and transient errors;
   log at debug/info. Same posture as locations.
5. **Backfill = same code path** via a CLI flag on the existing command (e.g.
   `-vin-only`), dry-run-able, restricted to `vin IS NULL` vehicles, so the
   current fleet converges in one run instead of over cron cycles.

## Backend changes (`api/`)

### B1. Telemetry — `service/telemetry_api.go`
- `VINFromVC(tenant, tokenID) (vin string, found bool, err error)`:
  per-vehicle JWT via `GetVehicleJWT`, then
  `query { vinVCLatest(tokenId: N) { vin } }` through `doQuery`.
  Distinguish "no VC / empty vin" (found=false, nil err) from transport
  errors. Add to the `TelemetryAPIService` interface + mock.

### B2. Sync — `service/license_plate_sync.go`
After the registration-document pass, when the effective VIN is still empty:
`VINFromVC` → found → `updates["vin"]` via the existing write path (dry-run
respected). `PlateSyncResult` gains `VINSource` (`registration | vc | none`)
for logging.

### B3. Cron/CLI — `cmd/fleet-lite-app/import_group_attestations.go`
- No structural change: the extended `SyncVehicle` runs where it runs today.
- Add `-vin-only` (Decision 5): per tenant, `SELECT ... WHERE vin IS NULL` →
  run just the VIN step. Fast, cheap, one-shot.

### B4. Nothing else
No migration (column exists), no config change, no frontend work
(`/vehicles` already returns `vin`; quick-view VIN row, list column, and
search already render it).

## Rollout

1. PR: B1–B3 + tests (mock telemetry client; testify).
2. Deploy, then one manual backfill run per env (`-dry-run` first, then live),
   `kubectl create job --from=cronjob/...` per the README pattern.
3. Cron keeps new vehicles converging; no further action.

## Edge cases / notes

- **No-VC / no-integration vehicles**: `vinVCLatest` returns nothing or the
  JWT exchange fails — skipped quietly, re-checked on future passes only while
  `vin IS NULL`. Cost is one cheap query/exchange per pass for a handful of
  vehicles; if that ever bothers us, add a `vin_checked_at` back-off column
  (deferred — schema-free v1). Generating missing VCs via attestation-api
  (`POST /v2/attestation/vin/:tokenId`, priv 5 — note that's
  `attestation-api.dimo.zone`, a different service from `attest.dimo.zone`)
  is a known fallback, deferred since almost all vehicles already have a VC.
- **Registration doc later disagrees with VC:** fill-if-missing means first
  writer wins; a mismatch is surfaceable later if it ever matters (unlikely).
- **DCX budget:** one-time ~fleet-size reads + per-new-vehicle. No recurring
  spend once converged.

## Open questions / to verify

1. `vinVCLatest` response when no VC exists — null vs GraphQL error — drives
   the found=false detection in B1. (One curl against a known no-VC vehicle
   settles it; low stakes since both shapes are easy to handle.)
