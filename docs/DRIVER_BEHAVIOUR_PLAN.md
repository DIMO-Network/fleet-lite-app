# Driver Behaviour from telemetry-api events — Plan (fleet-lite-app)

Surface DIMO **driver-behaviour events** (harsh braking, extreme braking, harsh
acceleration, harsh cornering) on the vehicle details screen: an at-a-glance
card and a 30-day trend for the vehicle, plus per-trip counts on every row of
the trips panel. All of it reads from telemetry-api queries the app already
authenticates for — no new permissions, no new storage.

Created: 2026-09-10
Status: BUILT (2026-09-10) — backend and frontend wired end to end on PR
#156; service layer verified live against 186612 (Ruptela) and 180895
(HashDog, unsupported) over a 30-day window; parser unit-tested on captured
payloads (`api/internal/service/testdata/`). Remaining: sign-in browser pass
on the details page (local tenant now carries the app license + the five
probe vehicles).

---

## Problem

The only place the app shows behaviour events today is the trip-replay modal,
as ticks on the scrubber. A fleet manager looking at a vehicle cannot see
whether it is driven roughly, whether that is getting better or worse, or which
trips were the bad ones. The data is already there: Ruptela units in the fleet
report thousands of these events a month.

## Event vocabulary (confirmed from source)

Event names are free strings in telemetry-api, not an enum. The canonical
list is `pkg/schema/spec/default-event-names.yaml` in
[DIMO-Network/model-garage](https://github.com/DIMO-Network/model-garage):

| Name | Category | In scope |
|---|---|---|
| `behavior.harshBraking` | driver behaviour | **yes** |
| `behavior.extremeBraking` | driver behaviour | **yes** |
| `behavior.harshAcceleration` | driver behaviour | **yes** |
| `behavior.harshCornering` | driver behaviour | **yes** |
| `safety.collision` | safety | no — never observed; revisit if a source emits it |
| `security.engineBlock` / `security.engineUnblock` | remote immobiliser | no — command echoes, not behaviour |

Trip start/end, charging session, seatbelt, airbag and driver-distraction
names exist in that file only as commented-out future work.

### Which connections emit events — this is the main constraint

model-garage registers an `EventModule` for **Ruptela**, **Kaufmann** (routes
its `r/` Ruptela family to the Ruptela converter, `kam/` to the default module)
and the **default** (pass-through) module. **AutoPi, Tesla and HashDog have no
event conversion at all** — vehicles on those connections will never have
behaviour data, no matter how long they are connected.

Ruptela derives events from its IO elements 135 (braking, low nibble = harsh,
high nibble = extreme), 136 (acceleration) and 143 (cornering). Each event
has `durationNs = 0` and metadata `{"counterValue": N}`.

Observed sources (`Event.source` is the connection's Ethereum address):

| Source | Connection | Events | Metadata shape |
|---|---|---|---|
| `0xF264…1CD0` | Ruptela | yes, high volume | `{"counterValue":1}` |
| `0x8D8c…19FD` | Kaufmann oracle | yes, low volume | `{"counterValue":1}` |
| `0xB8E6…3266` | "Vehicle Simulator" | yes | `{"speed":…,"yawRate":…}`, 60–120 s durations |
| HashDog / AutoPi / Tesla | — | **never** | — |

Design consequence: **treat `metadata` and `durationNs` as opaque.** Only
`name`, `timestamp` and counts are portable across sources.

## The read paths (verified 2026-09-10)

All three need privileges 1 + 4 (`GetNonLocationHistory` +
`GetLocationHistory`). `GetVehicleJWT` already exchanges for `[1, 3, 4, 5]`
(`gateway/dimo_auth_provider.go`) — **no permission changes needed**.

**1. All-time summary — one cheap call, the "at a glance" card.**

```graphql
dataSummary(tokenId: 186612) {
  firstSeen lastSeen
  eventDataSummary { name numberOfEvents firstSeen lastSeen }
}
```

Returns one row per event name the vehicle has *ever* reported. **Empty list
⇒ the connection does not emit events** — that is the gate for hiding the UI.

**2. Per-day counts — the trend chart and the per-distance rate.**

```graphql
dailyActivity(tokenId: 186612, from: "…", to: "…", mechanism: frequencyAnalysis,
  eventRequests: [{name:"behavior.harshBraking"}, {name:"behavior.extremeBraking"},
                  {name:"behavior.harshAcceleration"}, {name:"behavior.harshCornering"}]) {
  segmentCount duration
  eventCounts { name count }
  signals { name agg value }   # default set includes travelled-distance FIRST/LAST and speed MAX
}
```

Max 31 days. Returns one record per calendar day (zeros on idle days). The
default signal set already carries `powertrainTransmissionTravelledDistance`
FIRST/LAST, so **events per 100 km needs no extra request**.

**3. Per-trip counts — the trips panel.**

`segments` accepts the same `eventRequests` and returns `eventCounts` per
segment. This is the **same query the trips panel already runs**
(`telemetryAPIService.Segments`), so per-trip behaviour costs zero extra
telemetry calls — it is one more argument on an existing request.

`events(tokenId, from, to)` (the raw list) has **no limit or pagination**: a
30-day window on one Ruptela truck returned 3,198 rows. Keep it for the replay
modal's single-trip window only; never call it for the vehicle-level view.

## Live probe

Run against the app's own developer license (`DIMO_AUTH_CLIENT_ID` in
`api/settings.yaml`, privileged on five mainnet vehicles). 30-day counts:

| Token | Vehicle | Connection | harshBraking | extremeBraking | harshAccel | harshCornering |
|---|---|---|---|---|---|---|
| 186612 | Ford F-150 2025 | Ruptela | 683 | 1090 | 1213 | 212 |
| 117315 | Lexus NX 2021 | Ruptela | 59 | 142 | 154 | 194 |
| 192484 | Isuzu RBD+ 2026 | Kaufmann oracle | 0 | 0 | 1 | 2 |
| 192720 | Toyota 4Runner 2023 | Vehicle Simulator | 962 | 0 | 625 | 819 |
| 180895 | Toyota Camry 2025 | HashDog | — | — | — | — (no `eventDataSummary` rows) |

Two things this shows:

- **Volume differs by two orders of magnitude between vehicles.** Raw counts
  are not comparable; normalise to per-trip or per-100-km.
- The `dailyActivity` per-day `eventCounts` and `segments` per-trip
  `eventCounts` both came back populated with the default mechanism the app
  uses for aftermarket devices (`frequencyAnalysis`).

The probe program lives outside the repo (session scratchpad `eventprobe/`);
the pattern is: build `models.Tenant{ClientID: settings.DimoAuthClientID.Hex(),
DIMOPrivateKey: settings.DimoAuthPrivateKey}` and call
`authProvider.GetVehicleJWT`. The local DB tenant has no credentials and one
placeholder vehicle (token 100, an AutoPi Bolt) — useless for this feature.

## What already exists (reuse — do not rebuild)

| Capability | Location | Notes |
|---|---|---|
| Vehicle JWT with privs 1+4 (cached) | `gateway/dimo_auth_provider.go` `GetVehicleJWT` | reuse as-is |
| Telemetry GraphQL client | `service/telemetry_api.go` `query`/`doQuery` | add two query methods |
| Trips query with signal requests | `service/telemetry_api.go` `Segments` | **extend** with `eventRequests` + `eventCounts` |
| Trips endpoint, mechanism heuristic, permission handling | `controllers/telemetry.go` `GetSegments` | unchanged; response gains a field |
| Trips panel rows | `web/src/elements/vehicle-trips-panel.ts` `renderRow` | add event badges |
| Behaviour event colours + names | `web/src/elements/trip-replay-modal.ts` `EVENT_COLORS` | **lift** into a shared `utils/behavior-events.ts` |
| Details view sections + "Last 7 days" label | `web/src/views/vehicle-details.ts` ~L999 | new section slots next to trips |
| Unit-aware distance formatting | `PrefsService` + trip helpers | per-100-km vs per-62-mi follows user units |
| Permission-error → `permissionsRequired` | `controllers/telemetry.go` `isPermissionError` | same shape on the new endpoint |

## Design

### A. Per-trip behaviour (the trips panel) — answers "click a trip, see its behaviour"

1. `Segments` adds `eventRequests` for the four names and selects
   `eventCounts { name count }`. `Segment` gains `EventCounts []EventCount`
   (`json:"eventCounts"`). Existing callers ignore the new field.
2. `Trip` (web) gains `eventCounts?: Array<{name; count}>`.
3. `renderRow` shows a compact badge strip after the distance/speed line:
   one coloured pill per non-zero name (`EVENT_COLORS`), with a tooltip
   naming the event. Rows with all zeros show nothing — no "0 events" noise.
4. Selecting a trip (existing `selectTrip`) already expands geofence detail;
   add a one-line behaviour breakdown there using the same counts. The replay
   button keeps showing the events on the timeline (already implemented).

Cost: none. It is the same telemetry query with one more argument.

### B. Vehicle-level behaviour (new details section)

1. New service method `Behavior(tenant, tokenID, from, to, mechanism)` that
   runs **one** GraphQL request with two root fields: `dataSummary { … eventDataSummary }`
   and `dailyActivity(… eventRequests …)`. Returns:

   ```go
   type BehaviorSummary struct {
       Supported bool             `json:"supported"`   // eventDataSummary non-empty
       AllTime   []EventTotal     `json:"allTime"`     // name, count, firstSeen, lastSeen
       Days      []BehaviorDay    `json:"days"`        // date, counts per name, distanceKm, driveSeconds, tripCount
   }
   ```

   `distanceKm` = travelled-distance LAST − FIRST from the day's default
   signals (nil when either is missing — don't fabricate a rate from one end).
2. Endpoint `GET /telemetry/:tokenID/behavior?days=30` (cap 31, default 30).
   Same tenant/vehicle gate as `GetSegments`, same mechanism heuristic
   (aftermarket ⇒ `frequencyAnalysis`, else `ignitionDetection`), same
   `permissionsRequired` shape on JWT failure.
3. Web: `TelemetryService.behavior(tokenId, days)`; new
   `<vehicle-behavior-panel>` element rendered in `vehicle-details.ts` beside
   the trips panel. Contents, top to bottom:
   - Four stat tiles (one per event) with the 30-day count and, when distance
     is available, the rate per 100 km (or per 100 mi by user units).
   - A stacked daily bar chart, one bar per day, four series in `EVENT_COLORS`.
     Pure SVG in the element's `static styles`, like the existing telemetry
     charts — no charting library.
   - A footer line "First event <date> · <n> events all-time" from `allTime`.
4. **Gate:** `supported === false` ⇒ render a single muted line, *"This
   vehicle's connection doesn't report driving events."* Do not fetch
   `dailyActivity` for it on later loads (the summary call alone is enough
   to decide, but one request carries both, so just skip rendering).

Cost: one telemetry query per details-page open (DCX-billed). Acceptable;
matches what the trips panel already spends. No caching in v1 — revisit if
the details page becomes a polling surface.

### C. Not in scope

- Scoring / ranking drivers across the fleet (needs per-vehicle normalisation
  policy and a fleet-wide fan-out — a separate plan).
- Alerts on events (vehicle-triggers-api supports `telemetry.events`; see
  `docs/GEOFENCES_PLAN.md` webhook notes). Separate plan.
- Persisting event counts locally. Reads are cheap and bounded; storage adds a
  sync job for no user-visible gain today.
- Metadata (`counterValue`, `speed`, `yawRate`) — source-specific, hidden.

## Decisions

- **Three series in the UI, four names on the wire.** Harsh and extreme
  braking are collapsed into one "Harsh braking" series everywhere (tiles,
  stacked bars, legend, tooltip, trip pills, trip breakdown, replay ticks) —
  a fleet manager cares that the vehicle brakes hard, not which Ruptela
  threshold tripped. The API keeps returning the four canonical names;
  `BEHAVIOR_SERIES` in `web/src/utils/behavior-events.ts` does the folding, so
  the backend never needs to know about the grouping. Display/stack order:
  braking, cornering, acceleration.
- **Series colours are theme tokens** (`--bhv-braking`, `--bhv-cornering`,
  `--bhv-acceleration` in `global-styles.ts`), one set per theme, validated
  with the dataviz palette validator against the chart surface. The replay
  modal's old hard-coded hex values failed that check and now use the tokens.
- **Rate label is `/100 km`** (or `/100 mi`), not "per 100 km".
- **Tiles are a single column of three** beside the chart; per-trip pills show
  only non-zero series.
- **Gate on data, not on connection name.** The `vehicles` table does not
  store the device manufacturer or connection name, and the emitting set
  (Ruptela, Kaufmann `r/`, simulator, any future default-module source) is a
  moving target. An empty `eventDataSummary` is the truth. If copy needs to
  name the connection later, add `manufacturer { name }` /
  `connection { name }` to the identity sync — not a prerequisite.
- **Normalise by distance from `dailyActivity`'s default signals**, not by
  trip count: a 4-minute idle segment and a 6-hour haul are both "one trip".
- **Reuse `frequencyAnalysis` / `ignitionDetection` selection** from
  `GetSegments` so day boundaries and trip boundaries agree with the trips
  panel the user sees next to it. `dailyActivity` rejects the idling / refuel /
  recharge mechanisms; the heuristic never picks those.
- **One request for A and B each**, never `events` over a long window.

## Edge cases

- **Vehicle has a JWT but no location privilege**: `events` needs priv 4;
  `dataSummary`/`dailyActivity` fail the same way. Surface as
  `permissionsRequired`, like trips.
- **Vehicle emits events but has no travelled-distance signal** (some
  synthetic sources): show counts, omit the rate, no "per 0 km".
- **Ongoing trip**: `segments` still returns `eventCounts` for it; the row
  keeps the existing "Ongoing" marker.
- **> 31-day window**: telemetry-api rejects it. The endpoint clamps `days`.
- **Timezone**: pass the browser's IANA zone as `dailyActivity(timezone:)` so
  bars align with the user's calendar days; default is UTC.

## Open questions

- Should the trips panel's default 7-day window and the behaviour panel's
  30-day window be linked to one selector? Start independent; revisit after
  first use.

## Steps

Frontend (steps 3–4) is DONE against mock data; the response shapes it expects
are `BehaviorResponse` / `Trip.eventCounts` in `web/src/types/telemetry.ts`
and are the contract for the backend.

1. ~~`service/telemetry_api.go`: `EventCount` type; `Segments` adds
   `eventRequests` + `eventCounts`; new `Behavior` method + response types.
   Unit-test the response parsing with a captured payload from the probe.~~
   Done. Note: `dailyActivity` records carry **no date field** — the API
   returns exactly one per calendar day of the window (in `timezone`),
   oldest first — so `parseBehaviorResponse` derives dates from the window
   and fails loudly if the record count disagrees.
2. ~~`controllers/telemetry.go`: `GetBehavior` handler (`days` clamped to 31,
   `tz` passed to `dailyActivity(timezone:)`); route in `app/app.go` next to
   `segments`.~~ Done.
3. ~~Web: `utils/behavior-events.ts`; `Trip.eventCounts`; trip-row pills +
   expanded-row breakdown.~~ Done.
4. ~~Web: `TelemetryService.behavior`; `<vehicle-behavior-panel>`; mount in
   `vehicle-details.ts`.~~ Done, Spanish targets filled.
5. ~~Remove the review scaffolding.~~ Done.
6. Verify locally against token 186612 (Ruptela, dense) and 180895 (HashDog,
   should show the unsupported line) using the app license. Service layer
   done (30 days, `America/Bogota`: 30 records, dates aligned, per-trip
   `eventCounts` populated; 180895 → `supported:false`). Browser pass on the
   details page still to do — needs a signed-in wallet added to the local
   tenant.
