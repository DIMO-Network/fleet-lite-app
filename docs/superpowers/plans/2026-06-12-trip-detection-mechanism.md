# Configurable Trip Detection Mechanism Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user pick which telemetry-api `segments` detection mechanism powers the Trips card (currently hardcoded to `ignitionDetection`), persisted as a preference and round-tripped via a `mechanism` query param.

**Architecture:** Backend `Trips()` and `GetTrips` gain a validated `mechanism` parameter that's interpolated as a bare GraphQL enum literal into the existing `segments(...)` query. Frontend adds a `TripMechanism` preference (same localStorage pattern as the units toggle) and a `<select>` in the Trips card header that persists the choice and re-fetches trips.

**Tech Stack:** Go (Fiber) backend, Lit/TypeScript frontend. No new dependencies.

**Reference spec:** `docs/superpowers/specs/2026-06-12-trip-detection-mechanism-design.md`

---

## Note on testing approach

This codebase has no Go or TypeScript test suites — verification follows the
same convention as the trip-replay plan: each backend task verifies with
`go build`, each frontend task verifies with `npx tsc --noEmit`, and the
final task is a manual end-to-end check via `make dev`.

---

### Task 1: `DetectionMechanism` type and `Trips()` service method

**Files:**
- Modify: `api/internal/service/telemetry_api.go` (interface lines 37-51, `Trips` method lines 252-266 and 299-300)

- [ ] **Step 1: Add `DetectionMechanism` type, constants, and validator**

In `api/internal/service/telemetry_api.go`, immediately after the closing `}`
of the `TelemetryAPIService` interface (after line 51, before the `Trip`
struct comment on line 53), add:

```go

// DetectionMechanism is the segmentation strategy passed to telemetry-api's
// `segments` query as the `mechanism` enum argument. Values match the
// GraphQL enum's literal names exactly (interpolated unquoted into the query).
type DetectionMechanism string

const (
	MechanismIgnitionDetection    DetectionMechanism = "ignitionDetection"
	MechanismFrequencyAnalysis    DetectionMechanism = "frequencyAnalysis"
	MechanismChangePointDetection DetectionMechanism = "changePointDetection"
	MechanismIdling               DetectionMechanism = "idling"
	MechanismRefuel               DetectionMechanism = "refuel"
	MechanismRecharge              DetectionMechanism = "recharge"
)

// ValidDetectionMechanisms is the set of mechanisms accepted by the
// `mechanism` query param on GET /telemetry/:tokenID/trips.
var ValidDetectionMechanisms = []DetectionMechanism{
	MechanismIgnitionDetection,
	MechanismFrequencyAnalysis,
	MechanismChangePointDetection,
	MechanismIdling,
	MechanismRefuel,
	MechanismRecharge,
}

// IsValidDetectionMechanism reports whether s matches one of
// ValidDetectionMechanisms.
func IsValidDetectionMechanism(s string) bool {
	for _, m := range ValidDetectionMechanisms {
		if string(m) == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Add `mechanism` to the `Trips` interface method**

In the same file, in the `TelemetryAPIService` interface, replace:

```go
	// Trips queries detected driving segments (`segments`, ignition-based) for
	// a vehicle within [from, to]. The telemetry-api caps this range at 31 days.
	Trips(tenant models.Tenant, tokenID uint64, from, to string) ([]Trip, error)
```

with:

```go
	// Trips queries detected driving segments (`segments`) for a vehicle
	// within [from, to] using the given detection mechanism. The telemetry-api
	// caps this range at 31 days.
	Trips(tenant models.Tenant, tokenID uint64, from, to string, mechanism DetectionMechanism) ([]Trip, error)
```

- [ ] **Step 3: Add `mechanism` to the `Trips` implementation and query**

In the same file, replace the `Trips` method signature and query (currently):

```go
func (t *telemetryAPIService) Trips(tenant models.Tenant, tokenID uint64, from, to string) ([]Trip, error) {
	q := fmt.Sprintf(`query {
		segments(tokenId: %d, from: %q, to: %q, mechanism: ignitionDetection, limit: 100, signalRequests: [
			{name: "speed", agg: AVG},
			{name: "speed", agg: MAX},
			{name: "powertrainTransmissionTravelledDistance", agg: FIRST},
			{name: "powertrainTransmissionTravelledDistance", agg: LAST}
		]) {
			start { timestamp value { latitude longitude } }
			end { timestamp value { latitude longitude } }
			duration
			isOngoing
			signals { name agg value }
		}
	}`, tokenID, from, to)
```

with:

```go
func (t *telemetryAPIService) Trips(tenant models.Tenant, tokenID uint64, from, to string, mechanism DetectionMechanism) ([]Trip, error) {
	q := fmt.Sprintf(`query {
		segments(tokenId: %d, from: %q, to: %q, mechanism: %s, limit: 100, signalRequests: [
			{name: "speed", agg: AVG},
			{name: "speed", agg: MAX},
			{name: "powertrainTransmissionTravelledDistance", agg: FIRST},
			{name: "powertrainTransmissionTravelledDistance", agg: LAST}
		]) {
			start { timestamp value { latitude longitude } }
			end { timestamp value { latitude longitude } }
			duration
			isOngoing
			signals { name agg value }
		}
	}`, tokenID, from, to, mechanism)
```

Then, a few lines down, update the logging call from:

```go
	t.logger.Info().Uint64("tokenID", tokenID).Str("from", from).Str("to", to).
		Int("segments", len(resp.Data.Segments)).Msg("telemetry segments fetched")
```

to:

```go
	t.logger.Info().Uint64("tokenID", tokenID).Str("from", from).Str("to", to).
		Str("mechanism", string(mechanism)).Int("segments", len(resp.Data.Segments)).Msg("telemetry segments fetched")
```

- [ ] **Step 4: Build to verify it compiles**

Run: `cd api && go build ./...`
Expected: fails with something like `not enough arguments in call to t.telemetry.Trips` — this is expected, since the controller (Task 2) hasn't been updated yet. Confirm the error is specifically about the call site in `api/internal/controllers/telemetry.go`, not a syntax error in `telemetry_api.go`.

- [ ] **Step 5: Commit**

```bash
cd /Users/jamesli/DIMO/fleet-lite-app
git add api/internal/service/telemetry_api.go
git commit -m "feat(api): add DetectionMechanism type and parameterize Trips() segments query"
```

---

### Task 2: `GetTrips` controller validation and passthrough

**Files:**
- Modify: `api/internal/controllers/telemetry.go:177-223` (`GetTrips` handler)

- [ ] **Step 1: Parse and validate the `mechanism` query param**

In `api/internal/controllers/telemetry.go`, in `GetTrips`, immediately after
the `from` parsing block (after the `else if _, err := time.Parse(...)`
block that ends around line 206, before the `trips, err := t.telemetry.Trips(...)`
call on line 208), insert:

```go

	mechanism := service.DetectionMechanism(c.Query("mechanism"))
	if mechanism == "" {
		mechanism = service.MechanismIgnitionDetection
	} else if !service.IsValidDetectionMechanism(string(mechanism)) {
		return fiber.NewError(fiber.StatusBadRequest, "mechanism must be one of: ignitionDetection, frequencyAnalysis, changePointDetection, idling, refuel, recharge")
	}
```

- [ ] **Step 2: Pass `mechanism` to `Trips()`**

Replace:

```go
	trips, err := t.telemetry.Trips(tenant, tokenID, from, to)
```

with:

```go
	trips, err := t.telemetry.Trips(tenant, tokenID, from, to, mechanism)
```

- [ ] **Step 3: Build to verify it compiles**

Run: `cd api && go build ./...`
Expected: no output (clean build).

- [ ] **Step 4: Commit**

```bash
cd /Users/jamesli/DIMO/fleet-lite-app
git add api/internal/controllers/telemetry.go
git commit -m "feat(api): validate and pass mechanism query param to Trips()"
```

---

### Task 3: Frontend `TripMechanism` preference and service method

**Files:**
- Modify: `web/src/services/prefs-service.ts`
- Modify: `web/src/services/telemetry-service.ts`

- [ ] **Step 1: Add `TripMechanism` type, storage key, and getter/setter**

In `web/src/services/prefs-service.ts`, after the existing `UnitSystem` type
and constants:

```typescript
export type UnitSystem = 'imperial' | 'metric';

const STORAGE_KEY = 'fleet-lite:units';
const EVENT_NAME = 'fleet-lite-prefs-changed';
```

add:

```typescript

export type TripMechanism = 'ignitionDetection' | 'frequencyAnalysis' | 'changePointDetection' | 'idling' | 'refuel' | 'recharge';

const TRIP_MECHANISM_STORAGE_KEY = 'fleet-lite:trip-mechanism';
const VALID_TRIP_MECHANISMS: readonly TripMechanism[] = ['ignitionDetection', 'frequencyAnalysis', 'changePointDetection', 'idling', 'refuel', 'recharge'];
```

Then, inside the `PrefsService` class, after the `toggleUnits()` method and
before `subscribe()`, add:

```typescript

    /** Returns the persisted trip-detection mechanism (defaults to ignitionDetection). */
    public getTripMechanism(): TripMechanism {
        const v = localStorage.getItem(TRIP_MECHANISM_STORAGE_KEY);
        return (VALID_TRIP_MECHANISMS as readonly string[]).includes(v ?? '') ? (v as TripMechanism) : 'ignitionDetection';
    }

    public setTripMechanism(m: TripMechanism): void {
        localStorage.setItem(TRIP_MECHANISM_STORAGE_KEY, m);
        window.dispatchEvent(new CustomEvent(EVENT_NAME, { detail: { tripMechanism: m } }));
    }
```

- [ ] **Step 2: Add `mechanism` to `TelemetryService.trips()`**

In `web/src/services/telemetry-service.ts`, update the import at the top of
the file from:

```typescript
import { FleetLocationsResponse, LatestSignalsResponse, TimeSeriesResponse, TripsResponse, TripRouteResponse } from '../types/telemetry.ts';
```

to:

```typescript
import { FleetLocationsResponse, LatestSignalsResponse, TimeSeriesResponse, TripsResponse, TripRouteResponse } from '../types/telemetry.ts';
import { TripMechanism } from './prefs-service.ts';
```

Then replace the `trips()` method:

```typescript
    /**
     * GET /telemetry/:tokenId/trips — detected driving segments ("trips").
     * Omit `from`/`to` to get the server's default last-3-days window.
     */
    trips(tokenId: number, from?: string, to?: string): Promise<TripsResponse> {
        const q = new URLSearchParams();
        if (from) q.set('from', from);
        if (to) q.set('to', to);
        const suffix = q.toString() ? `?${q.toString()}` : '';
        return ApiService.getInstance().get<TripsResponse>(`/telemetry/${tokenId}/trips${suffix}`);
    }
```

with:

```typescript
    /**
     * GET /telemetry/:tokenId/trips — detected driving segments ("trips").
     * Omit `from`/`to` to get the server's default last-3-days window. `mechanism`
     * selects telemetry-api's segment-detection strategy (defaults server-side
     * to `ignitionDetection` if omitted).
     */
    trips(tokenId: number, from?: string, to?: string, mechanism?: TripMechanism): Promise<TripsResponse> {
        const q = new URLSearchParams();
        if (from) q.set('from', from);
        if (to) q.set('to', to);
        if (mechanism) q.set('mechanism', mechanism);
        const suffix = q.toString() ? `?${q.toString()}` : '';
        return ApiService.getInstance().get<TripsResponse>(`/telemetry/${tokenId}/trips${suffix}`);
    }
```

- [ ] **Step 3: Type-check to verify it compiles**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
cd /Users/jamesli/DIMO/fleet-lite-app
git add web/src/services/prefs-service.ts web/src/services/telemetry-service.ts
git commit -m "feat(web): add TripMechanism preference and telemetry-service param"
```

---

### Task 4: Trips card "Detection" dropdown in `vehicle-details.ts`

**Files:**
- Modify: `web/src/views/vehicle-details.ts`

- [ ] **Step 1: Import `TripMechanism`**

At the top of `web/src/views/vehicle-details.ts`, update:

```typescript
import { PrefsService } from '../services/prefs-service.ts';
```

to:

```typescript
import { PrefsService, TripMechanism } from '../services/prefs-service.ts';
```

- [ ] **Step 2: Extract a `fetchTrips()` helper and use it from `loadAll()`**

In `loadAll()`, replace:

```typescript
        const [latestRes, speedRes, distRes, tripsRes] = await Promise.allSettled([
            TelemetryService.getInstance().latest(tokenIdNum),
            TelemetryService.getInstance().timeSeries(tokenIdNum, 'speed', fromIso, toIso, '24h'),
            TelemetryService.getInstance().timeSeries(
                tokenIdNum,
                'powertrainTransmissionTravelledDistance',
                fromIso, toIso, '24h',
            ),
            TelemetryService.getInstance().trips(tokenIdNum),
        ]);

        if (latestRes.status === 'fulfilled') {
            this.latestSignals = latestRes.value.signals || {};
            this.telemetryPermissionsRequired = !!latestRes.value.permissionsRequired;
            this.telemetryDevLicense = latestRes.value.devLicense || '';
        } else {
            console.warn('latest telemetry failed', latestRes.reason);
        }
        if (speedRes.status === 'fulfilled') this.speedBuckets = speedRes.value.buckets || [];
        if (distRes.status === 'fulfilled')  this.distanceBuckets = distRes.value.buckets || [];
        if (tripsRes.status === 'fulfilled') {
            this.trips = [...(tripsRes.value.trips || [])]
                .sort((a, b) => Date.parse(b.startTime) - Date.parse(a.startTime));
        }
```

with:

```typescript
        const [latestRes, speedRes, distRes, tripsRes] = await Promise.allSettled([
            TelemetryService.getInstance().latest(tokenIdNum),
            TelemetryService.getInstance().timeSeries(tokenIdNum, 'speed', fromIso, toIso, '24h'),
            TelemetryService.getInstance().timeSeries(
                tokenIdNum,
                'powertrainTransmissionTravelledDistance',
                fromIso, toIso, '24h',
            ),
            this.fetchTrips(),
        ]);

        if (latestRes.status === 'fulfilled') {
            this.latestSignals = latestRes.value.signals || {};
            this.telemetryPermissionsRequired = !!latestRes.value.permissionsRequired;
            this.telemetryDevLicense = latestRes.value.devLicense || '';
        } else {
            console.warn('latest telemetry failed', latestRes.reason);
        }
        if (speedRes.status === 'fulfilled') this.speedBuckets = speedRes.value.buckets || [];
        if (distRes.status === 'fulfilled')  this.distanceBuckets = distRes.value.buckets || [];
        if (tripsRes.status === 'fulfilled') this.trips = tripsRes.value;
```

Then add the `fetchTrips()` helper and a change handler immediately after
`loadAll()`'s closing `}`:

```typescript

    /**
     * Fetches trips using the persisted detection-mechanism preference,
     * sorted newest-first. Used by `loadAll()` and the Trips card's
     * "Detection" dropdown.
     */
    private async fetchTrips(): Promise<Trip[]> {
        const tokenIdNum = Number(this.tokenId);
        const mechanism = PrefsService.getInstance().getTripMechanism();
        const res = await TelemetryService.getInstance().trips(tokenIdNum, undefined, undefined, mechanism);
        return [...(res.trips || [])].sort((a, b) => Date.parse(b.startTime) - Date.parse(a.startTime));
    }

    private async onMechanismChange(e: Event) {
        const value = (e.target as HTMLSelectElement).value as TripMechanism;
        PrefsService.getInstance().setTripMechanism(value);
        this.tripsExpanded = false;
        try {
            this.trips = await this.fetchTrips();
        } catch (err) {
            console.warn('trips failed', err);
            this.trips = [];
        }
    }
```

- [ ] **Step 3: Add the "Detection" dropdown to `renderTripsCard`**

In `renderTripsCard`, replace the header:

```typescript
        return html`
            <div class="trips-card">
                <div class="trips-header">
                    <h3>Trips</h3>
                    <span class="chip">Last 3 days</span>
                </div>
                ${body}
            </div>
        `;
```

with:

```typescript
        const mechanism = PrefsService.getInstance().getTripMechanism();
        return html`
            <div class="trips-card">
                <div class="trips-header">
                    <h3>Trips</h3>
                    <div class="trips-header-controls">
                        <select class="trip-mechanism-select" @change=${this.onMechanismChange} title="Trip detection method">
                            <option value="ignitionDetection" ?selected=${mechanism === 'ignitionDetection'}>Ignition</option>
                            <option value="frequencyAnalysis" ?selected=${mechanism === 'frequencyAnalysis'}>Frequency analysis</option>
                            <option value="changePointDetection" ?selected=${mechanism === 'changePointDetection'}>Change-point</option>
                            <option value="idling" ?selected=${mechanism === 'idling'}>Idling</option>
                            <option value="refuel" ?selected=${mechanism === 'refuel'}>Refuel</option>
                            <option value="recharge" ?selected=${mechanism === 'recharge'}>Recharge</option>
                        </select>
                        <span class="chip">Last 3 days</span>
                    </div>
                </div>
                ${body}
            </div>
        `;
```

This needs to be placed *before* the final `return html\`...\`` of
`renderTripsCard` — i.e. add the `const mechanism = ...` line right after the
`if`/`else if`/`else` block that sets `body` (the block ending at the `}`
before the final `return html`).

- [ ] **Step 4: Add `.trips-header-controls` and `.trip-mechanism-select` styling**

In the `static styles` block, find the `.trips-header .chip { ... }` rule
(it ends with a `}` on its own line). Immediately after that closing `}`,
add:

```typescript
            .trips-header-controls {
                display: flex;
                align-items: center;
                gap: 8px;
            }
            .trip-mechanism-select {
                background: var(--surface-container-high);
                border: none;
                color: var(--on-surface-variant);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                padding: 4px 8px;
                border-radius: var(--radius-sm);
                cursor: pointer;
            }
```

- [ ] **Step 5: Type-check to verify it compiles**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
cd /Users/jamesli/DIMO/fleet-lite-app
git add web/src/views/vehicle-details.ts
git commit -m "feat(web): add trip detection method dropdown to Trips card"
```

---

### Task 5: Manual end-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Start the dev stack**

Run: `make dev` (from the repo root) — brings up frontend (Vite, port 3009)
and backend (Go, port 8084) together. Wait for both to report ready.

- [ ] **Step 2: Verify the default request**

In a browser, navigate to `https://local-fleet-lite.dimo.org:3009`, sign in,
open a vehicle with trip history (e.g. token 186612, which has 56 completed
trips in the last 31 days — query with explicit `from`/`to` if the 3-day
default is empty). Open devtools Network tab, reload the page, and confirm
`GET /telemetry/186612/trips...` includes `mechanism=ignitionDetection` and
returns 200 with the existing trip list.

- [ ] **Step 3: Verify switching mechanisms**

In the Trips card header, change the "Detection" dropdown to "Frequency
analysis". Confirm:
- A new `GET /telemetry/186612/trips?mechanism=frequencyAnalysis` request
  fires
- The response is 200 (not 400)
- The trip list re-renders (the set of trips may differ from
  `ignitionDetection`'s results — that's expected)

Repeat for "Change-point", "Idling", "Refuel", and "Recharge" — confirm each
produces a 200 response with `mechanism=<value>` in the query string. For
"Idling"/"Refuel"/"Recharge", an empty trip list ("No trips in the last 3
days") is an acceptable result if the vehicle has no such segments in range —
the goal is confirming the request succeeds, not that data exists.

- [ ] **Step 4: Verify persistence**

With a non-default mechanism selected (e.g. "Frequency analysis"), reload the
page. Confirm the dropdown still shows "Frequency analysis" and the initial
`GET /telemetry/186612/trips` request includes `mechanism=frequencyAnalysis`.

- [ ] **Step 5: Verify invalid mechanism handling**

Using devtools or curl with the dev JWT, request
`GET /telemetry/186612/trips?mechanism=bogus`. Confirm a `400` response with
a message listing the valid mechanism values.

- [ ] **Step 6: Report results**

Note any visual or behavioral issues found. If everything works as expected,
the feature is complete — no further commits needed for this task.

---

## Self-Review Notes

- **Spec coverage:** Task 1 covers the `DetectionMechanism` type and
  `Trips()` query parameterization (spec §Backend Changes); Task 2 covers
  `GetTrips` validation/passthrough and the 400 error case (spec §Error
  Handling); Task 3 covers the `TripMechanism` preference and service method
  (spec §Frontend Changes, PrefsService/telemetry-service); Task 4 covers the
  dropdown UI, persistence, and re-fetch (spec §Frontend Changes,
  vehicle-details.ts); Task 5 covers manual verification of all six
  mechanisms, persistence, and the 400 path (spec §Testing).
- **Type consistency:** `DetectionMechanism` (Go) and `TripMechanism` (TS)
  both use the same six string literal values
  (`ignitionDetection`/`frequencyAnalysis`/`changePointDetection`/`idling`/`refuel`/`recharge`).
  `trips(tokenId, from?, to?, mechanism?)` signature matches between Task 3's
  service method and Task 4's `fetchTrips()`/`onMechanismChange()` usage.
  `fetchTrips(): Promise<Trip[]>` return type matches `this.trips: Trip[]`.
- **No placeholders:** every step contains complete, concrete code — no "add
  validation" or "similar to Task N" references.
- **Out of scope confirmed:** no `config` object, no mechanism-specific
  labels/empty-state copy, no changes to `TripRoute`/trip-replay — matches
  spec's Out of Scope section.
