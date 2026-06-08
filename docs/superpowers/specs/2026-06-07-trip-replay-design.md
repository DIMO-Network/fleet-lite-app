# Trip Replay (ported from rental-fleets-app)

**Date:** 2026-06-07
**Status:** Approved

## Summary

Add a "Replay" feature to the Trips card on the vehicle details page: clicking
"Replay" on a completed trip opens a modal showing an animated playback of the
vehicle's GPS route on a map, with play/pause/reset/speed controls and
color-coded markers for driving-behavior events (harsh braking, cornering,
acceleration). Ported from `rental-fleets-app`'s `trip-replay-modal-element.ts`
and adapted to fleet-lite-app's typed-endpoint backend, dark theme, and modal
conventions.

## Scope

- Backend: one new endpoint + service method + Go types
- Frontend: new types, one new service method, one new Lit element
  (`trip-replay-modal`), and a small integration change to the existing Trips
  card in `vehicle-details.ts`
- No new dependencies — Leaflet 1.9.4 and dayjs are already in `package.json`
  and already used in `fleet-overview.ts`

## Architecture & Data Flow

1. The trips list is already loaded client-side in `vehicle-details.ts`. A
   "Replay" button is added to each completed trip row (one with both
   `startTime` and a defined `endTime` — `!trip.isOngoing && trip.endTime`).
2. Clicking "Replay" sets `replayTrip = trip`, mounting `<trip-replay-modal>`
   with the `Trip` object and `tokenId` as properties. No new query is needed
   to populate the trips list itself.
3. On connect, the modal calls the new
   `GET /telemetry/:tokenID/trip-route?from=<trip.startTime>&to=<trip.endTime>`
   endpoint, which issues **one** GraphQL request to telemetry-api with
   aliased top-level fields (avoiding two round trips):

   ```graphql
   query {
     route: signals(tokenId: X, from: "...", to: "...", interval: "30s") {
       timestamp
       currentLocationCoordinates(agg: LAST) { latitude longitude }
     }
     events(tokenId: X, from: "...", to: "...") {
       timestamp
       name
       durationNs
     }
   }
   ```

   Both `events` and `currentLocationCoordinates` are present in the
   telemetry-api schema (`schema/events.graphqls`,
   `schema/signals-events_gen.graphqls`).
4. The endpoint returns `{ waypoints, events, from, to }`. The modal
   downsamples waypoints to 500 max, buckets events into timeline positions,
   and animates playback on a Leaflet map.

## Backend

**New types** (in `api/internal/service/telemetry_api.go`, alongside `Trip`):

```go
type TripWaypoint struct {
    Timestamp string  `json:"timestamp"`
    Lat       float64 `json:"lat"`
    Lng       float64 `json:"lng"`
}
type TripEvent struct {
    Timestamp  string `json:"timestamp"`
    Name       string `json:"name"`
    DurationNs int64  `json:"durationNs"`
}
```

**New service method**, following `Trips()`'s exact pattern (build query string
→ `t.query()` → unmarshal into anonymous struct → map to flat output → log
count):

```go
TripRoute(tenant models.Tenant, tokenID uint64, from, to string) ([]TripWaypoint, []TripEvent, error)
```

- Skips waypoints whose `currentLocationCoordinates` is null (no GPS fix that
  interval)
- Returns events as-is — no name filtering server-side; the frontend filters
  to known event names via its `EVENT_COLORS` lookup, matching
  rental-fleets-app's approach

**New controller handler** `GetTripRoute` in
`api/internal/controllers/telemetry.go`, mirroring `GetTrips`:

- Route: `GET /telemetry/:tokenID/trip-route?from=...&to=...`
- `from`/`to` are **required** RFC3339 timestamps (400 if missing/invalid) —
  unlike `GetTrips`, there is no default range, since replay always has an
  explicit trip window
- Validates vehicle-in-tenant via `vehicleInTenant`
- On `isPermissionError`, returns `200` with
  `{ waypoints: [], events: [], from, to, permissionsRequired: true, devLicense }`
- On success: `{ waypoints, events, from, to }`

**Route registration:** add
`telemetryGroup.Get("/:tokenID/trip-route", telemetryController.GetTripRoute)`
in `app.go`, next to the existing `/telemetry/:tokenID/trips` registration.

## Frontend

**Types** (added to `web/src/types/telemetry.ts`, alongside `Trip`/`TripsResponse`):

```typescript
export interface TripWaypoint { timestamp: string; lat: number; lng: number; }
export interface TripEvent { timestamp: string; name: string; durationNs: number; }
export interface TripRouteResponse {
    waypoints: TripWaypoint[];
    events: TripEvent[];
    from: string;
    to: string;
    permissionsRequired?: boolean;
}
```

**Service method** (in `telemetry-service.ts`, following the `trips()` pattern):

```typescript
tripRoute(tokenId: number, from: string, to: string): Promise<TripRouteResponse> {
    const q = new URLSearchParams({ from, to });
    return ApiService.getInstance().get<TripRouteResponse>(`/telemetry/${tokenId}/trip-route?${q.toString()}`);
}
```

**New element** `web/src/elements/trip-replay-modal.ts`, ported from
`rental-fleets-app/web/src/elements/trip-replay-modal-element.ts` and adapted
to fleet-lite-app conventions (overlay/card structure matching
`document-detail-modal.ts`):

- **Props:** `trip: Trip`, `tokenId: number`
- **On connect:** calls
  `TelemetryService.getInstance().tripRoute(tokenId, trip.startTime, trip.endTime!)`,
  downsamples waypoints to 500 max via the same `downsample()` step-skip
  helper, and buckets `events` into `eventFlags` (`{ name, pct }`) positioned
  along the timeline by elapsed-time percentage — filtered to the known
  `EVENT_COLORS` keys (`behavior.extremeBraking`, `harshBraking`,
  `harshCornering`, `harshAcceleration`)
- **Map:** Leaflet, using the **dark CartoDB tile layer**
  (`https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png` with the
  matching attribution string and `subdomains: 'abcd'`) copied verbatim from
  `fleet-overview.ts` for visual consistency with the rest of the app — faded
  full-route polyline (dashed, low opacity), green/red circle markers for
  start/end, an animated position marker, and a "drawn so far" polyline that
  grows as playback advances. Falls back to a simple start/end dashed-line map
  (`initFallbackMap`) when fewer than 2 waypoints exist.
- **Playback controls:** play/pause toggle, reset, 1×/2×/4× speed selector,
  progress bar with elapsed/total time display and color-coded event ticks
  with hover tooltips
- **Animation:** `setInterval` tick loop at `Math.floor(50 / speedMultiplier)`
  ms, advancing one waypoint per tick; updates marker position, drawn
  polyline, progress bar fill, and time display; stops automatically at the
  last waypoint
- **Close:** dispatches `new CustomEvent('close', { bubbles: true, composed: true })`,
  same as `document-detail-modal` and `upload-document-modal`
- **Cleanup:** `disconnectedCallback` clears the animation interval, the
  `mapInitTimer`, and removes the Leaflet map instance (mirrors
  `fleet-overview.ts`'s `disconnectedCallback`)
- **Styling:** CSS-in-JS via `static styles = [sharedStyles, css\`...\`]`,
  built on fleet-lite-app's dark-theme design tokens (`--surface-container`,
  `--outline-variant`, `--on-surface`, `--radius-lg`, `--type-label-caps`,
  etc.) and the `:host { position: fixed; inset: 0; ... }` overlay pattern,
  rather than rental-fleets-app's own palette/measurements. Imports
  `leaflet/dist/leaflet.css?inline` the same way `fleet-overview.ts` does.

**Integration into `vehicle-details.ts`:**

- Add `@state() private replayTrip: Trip | null = null;`
- Add a "Replay" button to `renderTripRow()`, rendered only when
  `!trip.isOngoing && trip.endTime` — sets `this.replayTrip = trip` on click
- Mount conditionally near the end of `render()`:
  ```html
  ${this.replayTrip ? html`<trip-replay-modal
          .trip=${this.replayTrip}
          .tokenId=${tokenIdNum}
          @close=${() => { this.replayTrip = null; }}>
      </trip-replay-modal>` : nothing}
  ```
- Import `'../elements/trip-replay-modal.ts'` at the top of the file, matching
  how `glovebox.ts` imports `document-detail-modal.ts`

## Error Handling

- If `tripRoute()` fails or returns `permissionsRequired: true`, the modal
  shows an inline message in place of the map (no map/animation initialized)
- If `waypoints.length < 2`, the fallback map renders start/end markers with a
  dashed connecting line and no playback controls (`hasControls` is false)
- Animation always stops cleanly at the final waypoint; no wraparound/loop

## Out of Scope

- No changes to the existing trips list query, sorting, or card layout beyond
  adding the Replay button
- No new npm/Go dependencies
- No persistence of replay state (speed, position) across modal open/close
