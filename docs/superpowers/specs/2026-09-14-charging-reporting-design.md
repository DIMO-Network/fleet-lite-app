# EV Charging Reporting — Design

Created: 2026-09-14

## Goal

Add a "Charging" tab that detects EV charging sessions from DIMO telemetry, maps
where charging happened, and reports per-session/per-vehicle/fleet-wide energy
delivered, electricity cost, and estimated $ saved versus an equivalent gasoline
cost.

## Scope

**In scope:**
- Server-side detection of charging sessions from telemetry signals (no new
  external integrations — everything comes from the existing DIMO Telemetry
  API, same auth path as trips/geofences).
- A fleet-wide map with one marker per detected charging session (location,
  clustered visually like the existing fleet vehicle map), plus a sessions
  table and per-vehicle drilldown.
- Tenant-level settings: electricity rate ($/kWh), gas price, and vehicle
  efficiency (kWh/mile or MPG-equivalent), used to compute cost and
  gas-comparison savings.
- Fleet and per-vehicle totals: kWh added, $ spent on electricity, $ saved vs.
  gasoline.
- CSV export (line-item, one row per session, fleet-wide or single-vehicle).

**Out of scope (this round):**
- Per-vehicle or per-session rate overrides (tenant-level only).
- Any charging-network/public-charger API integration — sessions are inferred
  purely from vehicle telemetry.
- Editing or annotating a detected session (e.g. renaming a location, marking
  it home vs. public).
- Combustion/hybrid vehicles that never report battery-charging signals —
  they're simply excluded from totals, not shown as zero.

## Data model

**New Postgres table** `charging_sessions` — one row per detected session,
persisted like geofence passes (past telemetry is immutable, so a computed
session never goes stale):

```sql
tenant_id        text
token_id         bigint
started_at       timestamptz
ended_at         timestamptz
added_energy_kwh numeric
avg_power_kw     numeric
soc_start_pct    numeric
soc_end_pct      numeric
lat              double precision
lng              double precision
num_samples      int
created_at
```

Plus a per-vehicle scan-coverage ledger (`charging_scan_coverage`:
`tenant_id`, `token_id`, `scanned_from`, `scanned_to`), same role as the
geofence detection ledger — a re-query only fetches telemetry for the gap
between what's already scanned and the requested window.

**New Postgres table** `tenant_charging_settings` — one optional row per
tenant:

```sql
tenant_id            text primary key
electricity_rate     numeric   -- $ per kWh
gas_price            numeric   -- $ per gallon
gas_mpg_equivalent   numeric   -- assumed miles-per-gallon for the comparison vehicle
vehicle_kwh_per_mile numeric   -- kWh consumed per mile driven (efficiency)
currency             text default 'USD'
created_at / updated_at
```

No row = charging tab still shows energy/kWh totals, but cost and
gas-savings columns are blank with a prompt to configure settings.

**Cost and savings are computed at read time**, never stored on the session
row:

- `electricity_cost = added_energy_kwh × electricity_rate`
- `miles_enabled = added_energy_kwh / vehicle_kwh_per_mile`
- `gas_cost_avoided = (miles_enabled / gas_mpg_equivalent) × gas_price`
- `savings = gas_cost_avoided − electricity_cost`

Recomputed live against current tenant settings every time sessions are
read, so editing a rate retroactively re-prices history — no backfill job.

## Backend (Go)

New service `internal/service/charging_detection_service.go`, structured like
`GeofenceDetectionService`: on each request, fetch only the telemetry gap not
yet covered by `charging_scan_coverage`, detect sessions in that gap, persist
them, then read back all persisted sessions overlapping the requested window.

Detection query — `signals(tokenId, from, to, interval: "30s")` requesting:
`powertrainTractionBatteryChargingIsCharging`,
`powertrainTractionBatteryChargingPower`,
`powertrainTractionBatteryChargingAddedEnergy`,
`powertrainTractionBatteryStateOfChargeCurrent`,
`currentLocationCoordinates(agg: LAST)`. A session is a contiguous run of
`isCharging = true` samples; `added_energy_kwh` is
`ChargingAddedEnergy` at the last sample minus the first (same
last-minus-first pattern `GeofenceDetectionService.engineRuntimeS` uses for
`obdRunTime`, including the same guard: a negative delta — counter reset mid-
session — drops the session's energy value to null rather than reporting a
negative number). Location is the last non-null
`currentLocationCoordinates` seen during the session. A session with fewer
than 2 samples is discarded (can't measure a delta).

`internal/service/tenant_charging_settings_service.go`: simple CRUD over
`tenant_charging_settings`, plus the read-time cost/savings computation
shared by the JSON and CSV endpoints so the numbers can't drift apart.

New controller `internal/controllers/charging.go`, following the
`vehicleInTenant` + `GetAllowedGroups(c)` pattern from `tco.go`/`documents.go`:

| Route | Purpose |
|---|---|
| `GET /charging/settings` | Fetch tenant rate/efficiency settings (empty if none) |
| `PUT /charging/settings` | Upsert `{electricityRate, gasPrice, gasMpgEquivalent, vehicleKwhPerMile, currency}` |
| `GET /charging/summary?from&to` | Fleet-wide: one point per session (tokenId, lat, lng, addedEnergyKwh, startedAt) for the map, plus fleet totals (kWh, cost, savings) |
| `GET /charging/:tokenId/sessions?from&to` | Single-vehicle session list with cost/savings per row |
| `GET /charging/export.csv?from&to` (optional `?tokenId=N`) | Streams line-item CSV, fleet-wide or single-vehicle |

Vehicles with no telemetry permission, or that never report
`powertrainTractionBatteryChargingIsCharging` (ICE vehicles, unsupported
connections), are silently excluded from `/charging/summary` and flagged
per-vehicle in `/charging/:tokenId/sessions` the same way TCO flags vehicles
without DIMO access — not an error.

## Frontend (Lit)

- New side-nav entry "Charging" → new view `web/src/views/charging-view.ts`.
- Map: reuses `utils/fleet-map.ts`'s `createFleetMap`/`applyTileTheme` and
  `leaflet.markercluster` setup — one marker per session, clustered
  identically to how vehicle markers cluster on the Fleet Overview map today.
  Clicking a marker opens that session's detail (vehicle, time, kWh, cost,
  savings).
- Fleet totals bar above the map: total kWh, total $ spent, total $ saved.
- Sessions table below the map (sortable by vehicle/date/kWh), row click ->
  per-vehicle drilldown consistent with the TCO view's row-click pattern.
- Settings panel (rate, gas price, MPG-equivalent, efficiency) editable
  inline, same shape as TCO's acquisition/depreciation edit panel.
- New service `web/src/services/charging-service.ts`: typed wrappers for the
  five `/charging/*` endpoints.
- New types file `web/src/types/charging.ts`.
- New cache `web/src/services/charging-cache.ts`, modeled on `tco-cache.ts`,
  so repeat tab loads within a tenant session skip re-fetching unchanged
  summary data.

## CSV export format

One row per session:

```
vehicle,vin,startedAt,endedAt,addedEnergyKwh,avgPowerKw,cost,gasCostAvoided,savings,currency
2023 Tesla Model 3,5YJ3E1EA1PF123456,2026-09-01T08:15:00Z,2026-09-01T09:42:00Z,32.4,7.1,4.86,11.20,6.34,USD
```

## Error handling

- No tenant settings configured: energy/kWh columns populate normally; cost,
  gas-cost-avoided, and savings columns render blank with a one-line prompt
  to configure settings (link to the settings panel), both in the UI and as
  empty CSV cells (not zeros — zero would misreport as "no savings").
- Vehicle has no location signal during a session: session still counts
  toward kWh/cost totals; simply has no map marker.
- Detection query failure for one vehicle (telemetry-api timeout, JWT
  exchange failure) does not fail the whole fleet summary — that vehicle is
  omitted from the response for that request and retried on the next load,
  same fan-out-isolation behavior as `FleetLocations`.

## Testing

- Go unit tests for the session-chunking algorithm in
  `charging_detection_service_test.go`: a session spanning the query
  boundary (partially covered by scan coverage), brief signal dropouts mid-
  session (isCharging flickers false for one sample), cable-connected-but-
  not-charging (should not open a session), an energy-counter reset mid-
  session (added-energy delta goes negative), a session with only one
  sample (discarded).
- Go unit tests for the cost/savings computation: zero/missing tenant
  settings, zero efficiency or zero MPG-equivalent (must not divide by
  zero).
- Controller tests for tenant-scoped settings CRUD and session-listing
  tenant/group access control, following `tco_service_test.go` conventions.
- Manual Chrome verification: load the Charging tab against a simulated or
  real EV with charging history, confirm map markers, session table, totals,
  and CSV export all agree with each other.
