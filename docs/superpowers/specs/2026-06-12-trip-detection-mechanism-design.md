# Configurable Trip Detection Mechanism

**Date:** 2026-06-12
**Status:** Approved

## Summary

The Trips card on the vehicle details page currently always asks telemetry-api
to detect driving segments using `mechanism: ignitionDetection`. This
mechanism doesn't always perform well (e.g. vehicles with unreliable ignition
signal reporting). Add a user-selectable "Detection" dropdown to the Trips
card that lets the user switch between all six `DetectionMechanism` values
telemetry-api's `segments` query supports. The selection is persisted as a
user preference (same pattern as the existing units toggle) and round-tripped
to the backend via a `mechanism` query param.

## Background: telemetry-api `segments` mechanisms

Per https://www.dimo.org/docs/api-references/telemetry-api/segments, the
`segments` query's `mechanism: DetectionMechanism!` argument accepts six enum
values:

- `ignitionDetection` — segments by `isIgnitionOn` state transitions (current
  default; most reliable when ignition signal support is good)
- `frequencyAnalysis` — signal-update-pattern based, backed by a materialized
  view (best perf for real-time scenarios)
- `changePointDetection` — CUSUM-based statistical change detection; DIMO
  describes it as a close match to the ignition baseline with good noise
  resistance
- `idling` — detects idle periods
- `refuel` — detects fuel-level-increase periods
- `recharge` — detects charge-level-increase periods

All six are exposed; `idling`/`refuel`/`recharge` produce segments with a
different real-world meaning than "trip" (idle time, refueling time), but the
response shape (`start`/`end`/`duration`/`isOngoing`/`signals`) is identical,
so no backend type changes are needed beyond the mechanism parameter itself.
This pass does not expose the optional `config` tuning object
(`maxGapSeconds`, `minSegmentDurationSeconds`, etc.) — telemetry-api's
defaults apply for every mechanism.

## Architecture & Data Flow

1. The Trips card header gets a "Detection" `<select>` with the six
   mechanisms.
2. The selected mechanism is persisted via `PrefsService` (localStorage),
   following the existing `getUnits`/`setUnits` pattern.
3. `vehicle-details.ts` reads the persisted mechanism on load and passes it to
   `TelemetryService.trips(tokenId, from, to, mechanism)`.
4. The frontend service appends `?mechanism=<value>` to
   `GET /telemetry/:tokenID/trips`.
5. `GetTrips` validates the `mechanism` query param against the six-value
   allow-list (defaults to `ignitionDetection` if omitted; 400 if present but
   invalid) and passes it to `Trips()`.
6. `Trips()` interpolates the mechanism as a bare GraphQL enum literal into
   the existing `segments(...)` query — same signal requests/shape as today,
   only the `mechanism:` value changes.
7. Changing the dropdown persists the new preference and re-fetches trips
   (not a full `loadAll()`).

## Backend Changes

**`api/internal/service/telemetry_api.go`:**

- New exported type and constants:
  ```go
  type DetectionMechanism string

  const (
      MechanismIgnitionDetection  DetectionMechanism = "ignitionDetection"
      MechanismFrequencyAnalysis  DetectionMechanism = "frequencyAnalysis"
      MechanismChangePointDetection DetectionMechanism = "changePointDetection"
      MechanismIdling             DetectionMechanism = "idling"
      MechanismRefuel             DetectionMechanism = "refuel"
      MechanismRecharge           DetectionMechanism = "recharge"
  )
  ```
  Plus a helper (e.g. `IsValidDetectionMechanism(string) bool`) checking
  membership in this set, used by the controller for validation.

- `TelemetryAPIService.Trips` interface signature gains a parameter:
  ```go
  Trips(tenant models.Tenant, tokenID uint64, from, to string, mechanism DetectionMechanism) ([]Trip, error)
  ```

- `Trips()` implementation: the query template's hardcoded
  `mechanism: ignitionDetection` becomes `mechanism: %s` with `mechanism`
  interpolated as a bare enum literal (not quoted — GraphQL enums are
  unquoted identifiers). The rest of the query (signal requests, response
  parsing, Trip mapping) is unchanged.

**`api/internal/controllers/telemetry.go`:**

- `GetTrips` reads `c.Query("mechanism")`. If empty, defaults to
  `service.MechanismIgnitionDetection`. If non-empty and
  `!service.IsValidDetectionMechanism(...)`, returns
  `fiber.NewError(fiber.StatusBadRequest, "mechanism must be one of: ...")`.
- Passes the validated mechanism to `t.telemetry.Trips(...)`.
- Response shape unchanged (`{trips, from, to}` / permissions-required shape).

## Frontend Changes

**`web/src/services/prefs-service.ts`:**

- New type: `export type TripMechanism = 'ignitionDetection' | 'frequencyAnalysis' | 'changePointDetection' | 'idling' | 'refuel' | 'recharge';`
- New storage key `fleet-lite:trip-mechanism`.
- `getTripMechanism(): TripMechanism` — defaults to `'ignitionDetection'` if
  unset or invalid (mirrors `getUnits`'s defaulting style).
- `setTripMechanism(m: TripMechanism): void` — writes localStorage and
  dispatches the existing `fleet-lite-prefs-changed` CustomEvent (consistent
  with `setUnits`, even though only `vehicle-details.ts` consumes it today).

**`web/src/services/telemetry-service.ts`:**

- `trips(tokenId: number, from?: string, to?: string, mechanism?: TripMechanism): Promise<TripsResponse>` —
  appends `mechanism` to the `URLSearchParams` when provided. Import
  `TripMechanism` from `prefs-service.ts`.

**`web/src/views/vehicle-details.ts`:**

- `loadAll()`: read `PrefsService.getInstance().getTripMechanism()` and pass
  it as the 4th arg to `trips(...)`.
- Trips card header (`renderTripsCard`, near the `<h3>Trips</h3>`): add a
  `<select class="trip-mechanism-select">` with options for the six
  mechanisms, labeled:
  - "Ignition" (`ignitionDetection`)
  - "Frequency analysis" (`frequencyAnalysis`)
  - "Change-point" (`changePointDetection`)
  - "Idling" (`idling`)
  - "Refuel" (`refuel`)
  - "Recharge" (`recharge`)

  `?selected=${mechanism === '...'}` per option, bound to
  `PrefsService.getInstance().getTripMechanism()`.
- `@change` handler: `PrefsService.getInstance().setTripMechanism(value)`,
  then re-fetch trips only (extract the trips fetch + state update from
  `loadAll()` into a small `loadTrips()` helper called by both `loadAll()`
  and the dropdown handler).
- Add `.trip-mechanism-select` styling consistent with existing small-control
  styles in this file (e.g. `.speed-select` in `trip-replay-modal.ts` is a
  close analog for a dark dropdown).

## Error Handling

- Invalid `mechanism` query param → `400 Bad Request` with a message listing
  valid values (same style as the existing `from`/`to` RFC3339 validation in
  `GetTrips`).
- Permission errors: unchanged — `GetTrips` still returns the
  `permissionsRequired` shape regardless of mechanism.
- If telemetry-api returns zero segments for a given mechanism (e.g. no
  idling/refuel events in range), the existing "No trips in the last 3 days"
  empty state renders as-is. (Label wording isn't mechanism-specific in this
  pass — out of scope, see below.)

## Testing

Matches this codebase's existing convention (no Go/TS test suites):
- `cd api && go build ./...` — clean build.
- `cd web && npx tsc --noEmit` — no errors.
- Manual check via `make dev`: switch the dropdown on a vehicle with trip
  history, confirm the `GET /telemetry/:tokenID/trips?mechanism=...` request
  fires with the new value, the trip list updates, and the preference
  survives a page reload.

## Out of Scope

- The optional `config` object (`maxGapSeconds`, `minSegmentDurationSeconds`,
  `signalCountThreshold`, `maxIdleRpm`, `minIncreasePercent`) — telemetry-api
  defaults apply for all mechanisms.
- Mechanism-specific labels/copy for the Trips card or empty state (e.g.
  renaming "Trips" to "Idle periods" when `idling` is selected).
- Cross-component sync of the preference (only `vehicle-details.ts` reads it
  today).
- `TripRoute` (trip-replay) is unaffected — it operates on a single
  already-selected trip's `[startTime, endTime]` window, independent of which
  mechanism produced that trip.
