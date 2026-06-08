# Trip Replay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **RESUME POINT (2026-06-08):** Tasks 1–4 are DONE and committed on branch `feat/trip-replay`
> (pushed to `origin/feat/trip-replay` — the original `feat/map-view` branch was deleted on
> remote, so the work was carried over to this new branch name). Commits, in order:
> - `358ea3d` Task 1: TripRoute service method + types
> - `514fc6b` Task 2: GetTripRoute controller handler + route registration
> - `d6c5652` Task 3: frontend TripRoute types + tripRoute service method
> - `d934c5c` Task 4: trip-replay-modal Lit element (verbatim port)
> - `922ca10` fix: guard trip-replay-modal against post-disconnect map/animation leak
>   (a Critical issue found in Task 4's code-quality review — closing the modal mid-fetch
>   could leave a live Leaflet map + tile requests + a runaway animation interval; fixed
>   with a `connected` flag checked at the 3 post-`await` resumption points in `fetchRoute`)
>
> Each of Tasks 1–4 went through the full subagent-driven-development cycle (implementer →
> spec-compliance review → code-quality review, with one fix-and-re-verify loop on Task 4).
> All reviews are now ✅. **Next: start at Task 5** (wire the Replay button into the Trips
> card), then Task 6 (manual e2e verification — needs `make dev` + a browser).

**Goal:** Add a "Replay" button to completed trips on the vehicle details page that opens a modal showing an animated GPS-route playback with behavior-event markers, ported from `rental-fleets-app`'s `trip-replay-modal-element.ts`.

**Architecture:** One new combined backend endpoint (`GET /telemetry/:tokenID/trip-route`) issues a single aliased GraphQL request to telemetry-api for both 30-second-interval location waypoints and behavior events, returning them as flat JSON. A new `trip-replay-modal` Lit element fetches this data on connect, downsamples waypoints to 500, and animates playback on a Leaflet map using the same dark CartoDB tiles as `fleet-overview.ts`.

**Tech Stack:** Go (Fiber) backend, Lit/TypeScript frontend, Leaflet 1.9.4 (already a dependency), dayjs (already a dependency). No new dependencies.

**Reference spec:** `docs/superpowers/specs/2026-06-07-trip-replay-design.md`

---

## Note on testing approach

This codebase has no Go or TypeScript test suites (`find api -name '*_test.go'` and `find web/src -iname '*.test.*'` both return nothing) — verification for the existing `Trips`/`GetTrips` work was "Go and TypeScript compile cleanly" plus manual testing against the running dev server (`make dev`, vehicle details page). This plan follows that same convention: each backend task verifies with `go build`, each frontend task verifies with `npx tsc --noEmit`, and the final task is a manual end-to-end check.

---

### Task 1: Backend types and `TripRoute` service method

**Files:**
- Modify: `api/internal/service/telemetry_api.go:37-48` (interface), and add new types/method near `Trip` (line 54) and `Trips` (line 233)

- [x] **Step 1: Add `TripWaypoint` and `TripEvent` types**

In `api/internal/service/telemetry_api.go`, immediately after the `Trip` struct (after line 64, before the `SignalLatest` comment on line 66), add:

```go
// TripWaypoint is one GPS fix sampled at a fixed interval across a trip's
// time window, used to animate route playback.
type TripWaypoint struct {
	Timestamp string  `json:"timestamp"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
}

// TripEvent is a discrete behavior event (e.g. harsh braking) within a
// trip's time window, used to mark the replay timeline.
type TripEvent struct {
	Timestamp  string `json:"timestamp"`
	Name       string `json:"name"`
	DurationNs int64  `json:"durationNs"`
}
```

- [x] **Step 2: Add `TripRoute` to the `TelemetryAPIService` interface**

In the same file, in the `TelemetryAPIService` interface (lines 37-48), add this method directly below the `Trips` line (after line 47, before the closing `}` on line 48):

```go
	// TripRoute fetches GPS waypoints (sampled every 30s) and behavior events
	// for a trip's [from, to] window, used to animate route playback.
	TripRoute(tenant models.Tenant, tokenID uint64, from, to string) ([]TripWaypoint, []TripEvent, error)
```

- [x] **Step 3: Implement `TripRoute`**

In the same file, immediately after the closing `}` of the `Trips` method (after line 324, before the `query` helper on line 326), add:

```go
// TripRoute queries `signals(..., interval: "30s")` for location waypoints
// and `events` for behavior markers within a trip's [from, to] window, in a
// single aliased GraphQL request. Waypoints with no GPS fix that interval
// are skipped; events are returned unfiltered (the frontend filters to known
// event names for display).
func (t *telemetryAPIService) TripRoute(tenant models.Tenant, tokenID uint64, from, to string) ([]TripWaypoint, []TripEvent, error) {
	q := fmt.Sprintf(`query {
		route: signals(tokenId: %d, from: %q, to: %q, interval: "30s") {
			timestamp
			currentLocationCoordinates(agg: LAST) { latitude longitude }
		}
		events(tokenId: %d, from: %q, to: %q) {
			timestamp
			name
			durationNs
		}
	}`, tokenID, from, to, tokenID, from, to)

	raw, err := t.query(tenant, tokenID, q)
	if err != nil {
		return nil, nil, err
	}

	var resp struct {
		Data struct {
			Route []struct {
				Timestamp                 string `json:"timestamp"`
				CurrentLocationCoordinates *struct {
					Latitude  float64 `json:"latitude"`
					Longitude float64 `json:"longitude"`
				} `json:"currentLocationCoordinates"`
			} `json:"route"`
			Events []struct {
				Timestamp  string `json:"timestamp"`
				Name       string `json:"name"`
				DurationNs int64  `json:"durationNs"`
			} `json:"events"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, fmt.Errorf("parse trip route: %w", err)
	}

	waypoints := make([]TripWaypoint, 0, len(resp.Data.Route))
	for _, pt := range resp.Data.Route {
		if pt.CurrentLocationCoordinates == nil {
			continue
		}
		waypoints = append(waypoints, TripWaypoint{
			Timestamp: pt.Timestamp,
			Lat:       pt.CurrentLocationCoordinates.Latitude,
			Lng:       pt.CurrentLocationCoordinates.Longitude,
		})
	}

	events := make([]TripEvent, 0, len(resp.Data.Events))
	for _, e := range resp.Data.Events {
		events = append(events, TripEvent{Timestamp: e.Timestamp, Name: e.Name, DurationNs: e.DurationNs})
	}

	t.logger.Info().Uint64("tokenID", tokenID).Str("from", from).Str("to", to).
		Int("waypoints", len(waypoints)).Int("events", len(events)).Msg("telemetry trip route fetched")

	return waypoints, events, nil
}
```

- [x] **Step 4: Build to verify it compiles**

Run: `cd api && go build ./...`
Expected: no output (clean build). If it fails with "missing method TripRoute", check that the interface (Step 2) and implementation (Step 3) method signatures match exactly.

- [x] **Step 5: Commit**

```bash
cd /Users/jamesli/DIMO/fleet-lite-app
git add api/internal/service/telemetry_api.go
git commit -m "feat(api): add TripRoute service method for trip replay waypoints and events"
```

---

### Task 2: Backend `GetTripRoute` controller handler and route registration

**Files:**
- Modify: `api/internal/controllers/telemetry.go` (add handler after `GetTrips`, currently ending at line 223)
- Modify: `api/internal/app/app.go:114` (route registration)

- [x] **Step 1: Add the `GetTripRoute` handler**

In `api/internal/controllers/telemetry.go`, immediately after the closing `}` of `GetTrips` (after line 223), add:

```go

// GetTripRoute — GET /telemetry/:tokenID/trip-route?from=...&to=...
// Returns GPS waypoints and behavior events for a trip's time window, used
// to animate route playback. Unlike GetTrips, `from`/`to` are required —
// replay always has an explicit window from the selected trip.
func (t *TelemetryController) GetTripRoute(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	if !t.vehicleInTenant(c.Context(), tenant.ID, tokenID) {
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}

	from := c.Query("from")
	if from == "" {
		return fiber.NewError(fiber.StatusBadRequest, "from is required and must be an RFC3339 timestamp")
	}
	if _, err := time.Parse(time.RFC3339, from); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "from must be an RFC3339 timestamp")
	}

	to := c.Query("to")
	if to == "" {
		return fiber.NewError(fiber.StatusBadRequest, "to is required and must be an RFC3339 timestamp")
	}
	if _, err := time.Parse(time.RFC3339, to); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "to must be an RFC3339 timestamp")
	}

	waypoints, events, err := t.telemetry.TripRoute(tenant, tokenID, from, to)
	if err != nil {
		if isPermissionError(err) {
			return c.JSON(fiber.Map{
				"waypoints":           []service.TripWaypoint{},
				"events":              []service.TripEvent{},
				"from":                from,
				"to":                  to,
				"permissionsRequired": true,
				"devLicense":          tenant.ClientID,
			})
		}
		t.logger.Err(err).Uint64("tokenID", tokenID).Msg("telemetry trip route failed")
		return fiber.NewError(fiber.StatusBadGateway, "telemetry trip route failed: "+err.Error())
	}
	return c.JSON(fiber.Map{"waypoints": waypoints, "events": events, "from": from, "to": to})
}
```

- [x] **Step 2: Register the route**

In `api/internal/app/app.go`, immediately after line 114 (`tenantApp.Get("/telemetry/:tokenID/trips", telemetryCtrl.GetTrips)`), add:

```go
	tenantApp.Get("/telemetry/:tokenID/trip-route", telemetryCtrl.GetTripRoute)
```

- [x] **Step 3: Build to verify it compiles**

Run: `cd api && go build ./...`
Expected: no output (clean build).

- [x] **Step 4: Commit**

```bash
cd /Users/jamesli/DIMO/fleet-lite-app
git add api/internal/controllers/telemetry.go api/internal/app/app.go
git commit -m "feat(api): expose GET /telemetry/:tokenID/trip-route endpoint"
```

---

### Task 3: Frontend types and `tripRoute` service method

**Files:**
- Modify: `web/src/types/telemetry.ts` (add types after `TripsResponse`, currently lines 37-39)
- Modify: `web/src/services/telemetry-service.ts` (add method after `trips()`)

- [x] **Step 1: Add `TripWaypoint`, `TripEvent`, `TripRouteResponse` types**

In `web/src/types/telemetry.ts`, immediately after the `TripsResponse` interface closing brace, add:

```typescript
export interface TripWaypoint {
    timestamp: string;
    lat: number;
    lng: number;
}

export interface TripEvent {
    timestamp: string;
    name: string;
    durationNs: number;
}

export interface TripRouteResponse {
    waypoints: TripWaypoint[];
    events: TripEvent[];
    from: string;
    to: string;
    permissionsRequired?: boolean;
}
```

- [x] **Step 2: Add the `tripRoute` service method**

In `web/src/services/telemetry-service.ts`, immediately after the `trips()` method's closing brace, add:

```typescript

    /**
     * GET /telemetry/:tokenId/trip-route — GPS waypoints and behavior events
     * for a trip's time window, used to animate route playback. `from`/`to`
     * are required (the trip's start/end timestamps).
     */
    tripRoute(tokenId: number, from: string, to: string): Promise<TripRouteResponse> {
        const q = new URLSearchParams({ from, to });
        return ApiService.getInstance().get<TripRouteResponse>(`/telemetry/${tokenId}/trip-route?${q.toString()}`);
    }
```

Also update the import at the top of the file to include `TripRouteResponse`:

```typescript
import { FleetLocationsResponse, LatestSignalsResponse, TimeSeriesResponse, TripsResponse, TripRouteResponse } from '../types/telemetry.ts';
```

- [x] **Step 3: Type-check to verify it compiles**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [x] **Step 4: Commit**

```bash
cd /Users/jamesli/DIMO/fleet-lite-app
git add web/src/types/telemetry.ts web/src/services/telemetry-service.ts
git commit -m "feat(web): add TripRoute types and telemetry-service method"
```

---

### Task 4: `trip-replay-modal` Lit element

**Files:**
- Create: `web/src/elements/trip-replay-modal.ts`

This is the core ported component — adapted from `rental-fleets-app/web/src/elements/trip-replay-modal-element.ts` to fleet-lite-app's `Trip` shape (flat `startTime`/`endTime`/`distanceKm`/`avgSpeedKph`/`maxSpeedKph`/`duration` fields rather than nested `start.value`/`signals[]`), its `TelemetryService` singleton (rather than raw GraphQL POST + `@lit/context`), its dark CartoDB tiles (rather than OSM), its modal/card/`sharedStyles` conventions (rather than `globalStyles`/`.replay-modal`), and its `formatDistance`/`formatSpeed` unit-aware utilities for the stats bar.

- [x] **Step 1: Create the file with imports, types, and helpers**

Create `web/src/elements/trip-replay-modal.ts`:

```typescript
import { LitElement, html, css, nothing, unsafeCSS } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import L from 'leaflet';
import leafletCss from 'leaflet/dist/leaflet.css?inline';
import dayjs from 'dayjs';
import { sharedStyles } from '../global-styles.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { Trip, TripWaypoint, TripEvent } from '../types/telemetry.ts';
import { formatDistance, formatSpeed } from '../utils/units.ts';

interface EventFlag {
    name: string;
    pct: number;
}

const MAX_WAYPOINTS = 500;
const TILE_URL = 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png';
const TILE_ATTRIBUTION = '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>';

const EVENT_COLORS: Readonly<Record<string, string>> = {
    'behavior.extremeBraking': '#EF4444',
    'behavior.harshBraking': '#F59E0B',
    'behavior.harshCornering': '#A78BFA',
    'behavior.harshAcceleration': '#34D399',
};

function downsample(pts: TripWaypoint[]): TripWaypoint[] {
    if (pts.length <= MAX_WAYPOINTS) return pts;
    const step = Math.ceil(pts.length / MAX_WAYPOINTS);
    const out: TripWaypoint[] = [];
    for (let i = 0; i < pts.length; i += step) out.push(pts[i]);
    if (out[out.length - 1] !== pts[pts.length - 1]) out.push(pts[pts.length - 1]);
    return out;
}

function fmtDuration(seconds: number): string {
    const h = Math.floor(seconds / 3600);
    const m = Math.round((seconds % 3600) / 60);
    return h > 0 ? (m > 0 ? `${h}h ${m}m` : `${h}h`) : `${m}m`;
}
```

- [x] **Step 2: Add the `@customElement` class declaration with styles**

Append to the same file:

```typescript

@customElement('trip-replay-modal')
export class TripReplayModal extends LitElement {
    static styles = [
        sharedStyles,
        unsafeCSS(leafletCss),
        css`
            :host {
                position: fixed;
                inset: 0;
                z-index: 100;
                display: flex;
                align-items: center;
                justify-content: center;
                background: rgba(0, 0, 0, 0.6);
                backdrop-filter: blur(4px);
            }
            .card {
                width: min(90vw, 820px);
                max-height: 90vh;
                display: flex;
                flex-direction: column;
                overflow: hidden;
                background: var(--surface-container);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                color: var(--on-surface);
                position: relative;
            }
            .replay-header {
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 16px 24px;
                border-bottom: 1px solid var(--outline-variant);
                flex-shrink: 0;
            }
            .replay-title { font: var(--type-headline-md); }
            .replay-subtitle {
                display: block;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-top: 4px;
            }
            .close {
                background: none;
                border: none;
                color: var(--on-surface-variant);
                padding: 4px;
                cursor: pointer;
            }
            .close:hover { color: var(--primary); }
            .map-wrapper { position: relative; flex-shrink: 0; }
            #map { width: 100%; height: 340px; background: var(--surface-container-lowest); }
            .sparse-msg {
                position: absolute;
                bottom: 10px;
                left: 50%;
                transform: translateX(-50%);
                background: rgba(0, 0, 0, 0.8);
                border: 1px solid var(--secondary);
                border-radius: var(--radius-sm);
                padding: 6px 12px;
                font: var(--type-label-caps);
                color: var(--secondary);
                pointer-events: none;
                white-space: nowrap;
            }
            .map-state {
                height: 340px;
                display: flex;
                align-items: center;
                justify-content: center;
                font: var(--type-body-md);
                color: var(--on-surface-variant);
            }
            .map-state.error { color: var(--error); }
            .stats-bar {
                display: flex;
                border-top: 1px solid var(--outline-variant);
                border-bottom: 1px solid var(--outline-variant);
                flex-shrink: 0;
            }
            .stat {
                flex: 1;
                padding: 12px 20px;
                border-right: 1px solid var(--outline-variant);
            }
            .stat:last-child { border-right: none; }
            .stat-label {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-bottom: 4px;
            }
            .stat-value { font: var(--type-headline-md); font-size: 18px; }
            .stat-value .unit { font-size: 12px; font-weight: 400; color: var(--on-surface-variant); margin-left: 2px; }
            .controls {
                display: flex;
                align-items: center;
                gap: 12px;
                padding: 12px 20px;
                flex-shrink: 0;
            }
            .progress-bar {
                flex: 1;
                position: relative;
                height: 4px;
                overflow: visible;
            }
            .progress-track {
                height: 100%;
                background: var(--outline-variant);
                border-radius: var(--radius-full);
                overflow: hidden;
            }
            .progress-fill {
                height: 100%;
                background: var(--primary);
                border-radius: var(--radius-full);
            }
            .event-tick {
                position: absolute;
                top: -4px;
                width: 2px;
                height: 12px;
                background: var(--tick-color);
                border-radius: 1px;
                transform: translateX(-50%);
                cursor: default;
            }
            .event-tick-tooltip {
                display: none;
                position: absolute;
                bottom: 16px;
                left: 50%;
                transform: translateX(-50%);
                background: var(--surface-container-lowest);
                border: 1px solid var(--tick-color);
                border-radius: var(--radius-sm);
                padding: 3px 7px;
                font: var(--type-label-caps);
                font-size: 9px;
                color: var(--tick-color);
                white-space: nowrap;
                pointer-events: none;
            }
            .event-tick:hover .event-tick-tooltip { display: block; }
            .time-display {
                font: var(--type-label-caps);
                color: var(--on-surface-variant);
                white-space: nowrap;
            }
            .ctrl-btn {
                background: var(--surface-container-high);
                border: none;
                border-radius: var(--radius-sm);
                width: 32px;
                height: 32px;
                cursor: pointer;
                display: flex;
                align-items: center;
                justify-content: center;
                color: var(--on-surface);
                font-size: 14px;
                flex-shrink: 0;
            }
            .ctrl-btn.primary { background: var(--primary); color: var(--on-primary); }
            .ctrl-btn:hover { opacity: 0.8; }
            .speed-select {
                background: var(--surface-container-high);
                border: none;
                color: var(--on-surface-variant);
                font: var(--type-label-caps);
                padding: 4px 8px;
                border-radius: var(--radius-sm);
                cursor: pointer;
            }
        `,
    ];
}
```

- [x] **Step 3: Add properties, state, private fields, and lifecycle methods**

Insert inside the class body (after `static styles = [...]`):

```typescript

    @property({ attribute: false }) trip!: Trip;
    @property({ type: Number }) tokenId!: number;

    @state() private waypoints: TripWaypoint[] = [];
    @state() private isPlaying = false;
    @state() private speedMultiplier: 1 | 2 | 4 = 1;
    @state() private loading = true;
    @state() private fetchError = '';
    @state() private isSparse = false;
    @state() private eventFlags: EventFlag[] = [];

    private currentStep = 0;
    private map?: L.Map;
    private positionMarker?: L.CircleMarker;
    private drawnPolyline?: L.Polyline;
    private animationInterval?: number;
    private mapInitTimer?: number;

    private readonly onKeydown = (e: KeyboardEvent) => { if (e.key === 'Escape') this.dispatchClose(); };

    override connectedCallback() {
        super.connectedCallback();
        document.addEventListener('keydown', this.onKeydown);
        void this.fetchRoute();
    }

    override disconnectedCallback() {
        super.disconnectedCallback();
        document.removeEventListener('keydown', this.onKeydown);
        if (this.mapInitTimer !== undefined) { clearTimeout(this.mapInitTimer); this.mapInitTimer = undefined; }
        this.stopAnim();
        this.map?.remove();
        this.map = undefined;
    }

    private dispatchClose() {
        this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
    }
```

- [x] **Step 4: Add `fetchRoute` (data loading)**

Insert after `dispatchClose`:

```typescript

    private async fetchRoute() {
        this.loading = true;
        this.fetchError = '';
        try {
            const resp = await TelemetryService.getInstance().tripRoute(this.tokenId, this.trip.startTime, this.trip.endTime!);

            if (resp.permissionsRequired) {
                this.fetchError = 'Grant DIMO permissions on this vehicle to see trip replay.';
                this.loading = false;
                return;
            }

            const raw = [...resp.waypoints].sort((a, b) => a.timestamp.localeCompare(b.timestamp));
            this.waypoints = downsample(raw);

            if (this.waypoints.length < 2) {
                this.isSparse = true;
                this.loading = false;
                await this.updateComplete;
                this.initFallbackMap();
                return;
            }

            const startMs = new Date(this.trip.startTime).getTime();
            const endMs = new Date(this.trip.endTime!).getTime();
            const range = endMs - startMs;
            this.eventFlags = range <= 0 ? [] : resp.events
                .filter((e) => e.name in EVENT_COLORS)
                .map((e) => ({
                    name: e.name,
                    pct: Math.min(100, Math.max(0, (new Date(e.timestamp).getTime() - startMs) / range * 100)),
                }));

            this.loading = false;
            await this.updateComplete;
            this.initMap();
        } catch (e) {
            this.fetchError = e instanceof Error ? e.message : 'Failed to load GPS data';
            this.loading = false;
        }
    }
```

- [x] **Step 5: Add map initialization methods**

Insert after `fetchRoute`:

```typescript

    private initFallbackMap() {
        const el = this.shadowRoot?.getElementById('map');
        if (!el || !this.trip.startLocation || !this.trip.endLocation) return;
        const { lat: sLat, lon: sLng } = this.trip.startLocation;
        const { lat: eLat, lon: eLng } = this.trip.endLocation;

        this.map = L.map(el as HTMLElement);
        L.tileLayer(TILE_URL, { attribution: TILE_ATTRIBUTION, subdomains: 'abcd', maxZoom: 19 }).addTo(this.map);

        L.circleMarker([sLat, sLng], { color: '#39FF14', fillColor: '#39FF14', fillOpacity: 0.9, radius: 8 })
            .bindPopup('Start').addTo(this.map);
        L.circleMarker([eLat, eLng], { color: '#FF0055', fillColor: '#FF0055', fillOpacity: 0.9, radius: 8 })
            .bindPopup('End').addTo(this.map);
        L.polyline([[sLat, sLng], [eLat, eLng]], { color: '#3388ff', dashArray: '6,6', opacity: 0.5, weight: 2 }).addTo(this.map);

        try { this.map.fitBounds([[sLat, sLng], [eLat, eLng]], { padding: [40, 40] }); } catch { /* ignore */ }
        this.mapInitTimer = window.setTimeout(() => {
            this.mapInitTimer = undefined;
            this.map?.invalidateSize();
        }, 100);
    }

    private initMap() {
        const el = this.shadowRoot?.getElementById('map');
        if (!el || this.waypoints.length < 2) return;

        const bounds = this.waypoints.map((w) => [w.lat, w.lng] as [number, number]);

        this.map = L.map(el as HTMLElement);
        L.tileLayer(TILE_URL, { attribution: TILE_ATTRIBUTION, subdomains: 'abcd', maxZoom: 19 }).addTo(this.map);

        L.polyline(bounds, { color: '#3388ff', opacity: 0.2, weight: 2, dashArray: '4,3' }).addTo(this.map);

        L.circleMarker(bounds[0], { color: '#39FF14', fillColor: '#39FF14', fillOpacity: 0.9, radius: 8 })
            .bindPopup('Start').addTo(this.map);
        L.circleMarker(bounds[bounds.length - 1], { color: '#FF0055', fillColor: '#FF0055', fillOpacity: 0.9, radius: 8 })
            .bindPopup('End').addTo(this.map);

        this.drawnPolyline = L.polyline([], { color: '#3388ff', weight: 2.5, opacity: 0.9 }).addTo(this.map);
        this.positionMarker = L.circleMarker(bounds[0], { color: '#3388ff', fillColor: '#3388ff', fillOpacity: 1, radius: 7 }).addTo(this.map);

        try { this.map.fitBounds(bounds as L.LatLngBoundsLiteral, { padding: [40, 40] }); } catch { /* ignore */ }
        this.mapInitTimer = window.setTimeout(() => {
            this.mapInitTimer = undefined;
            this.map?.invalidateSize();
            this.startAnim();
        }, 150);
    }
```

- [x] **Step 6: Add playback control methods and getters**

Insert after `initMap`:

```typescript

    private startAnim() {
        if (this.animationInterval !== undefined) clearInterval(this.animationInterval);
        this.animationInterval = window.setInterval(() => this.tick(), Math.floor(50 / this.speedMultiplier));
        this.isPlaying = true;
    }

    private stopAnim() {
        if (this.animationInterval !== undefined) { clearInterval(this.animationInterval); this.animationInterval = undefined; }
        this.isPlaying = false;
    }

    private tick() {
        if (this.currentStep >= this.waypoints.length - 1) { this.stopAnim(); return; }
        this.currentStep++;
        const wp = this.waypoints[this.currentStep];
        this.positionMarker?.setLatLng([wp.lat, wp.lng]);
        this.drawnPolyline?.addLatLng([wp.lat, wp.lng]);
        this.requestUpdate();
    }

    private togglePlay() {
        if (this.isPlaying) { this.stopAnim(); }
        else { if (this.currentStep >= this.waypoints.length - 1) this.doReset(); this.startAnim(); }
    }

    private doReset() {
        this.stopAnim();
        this.currentStep = 0;
        this.drawnPolyline?.setLatLngs([]);
        if (this.waypoints.length > 0) this.positionMarker?.setLatLng([this.waypoints[0].lat, this.waypoints[0].lng]);
        this.requestUpdate();
    }

    private onSpeedChange(e: Event) {
        this.speedMultiplier = Number((e.target as HTMLSelectElement).value) as 1 | 2 | 4;
        if (this.isPlaying) { this.stopAnim(); this.startAnim(); }
    }

    private get progressPct(): number {
        return this.waypoints.length > 1 ? (this.currentStep / (this.waypoints.length - 1)) * 100 : 0;
    }
    private get currentTs(): string {
        return this.waypoints.length ? dayjs(this.waypoints[this.currentStep].timestamp).format('HH:mm') : '';
    }
    private get endTs(): string {
        return this.trip.endTime ? dayjs(this.trip.endTime).format('HH:mm') : '';
    }
```

**Note on the `tick()`/`requestUpdate()` change from the original:** rental-fleets-app's `tick()` directly mutates `.progress-fill` width and `.time-display` text via `querySelector` to avoid a full Lit re-render every 50ms. This port instead calls `this.requestUpdate()`, which re-renders only the parts of the template that read reactive state/getters — simpler and consistent with this codebase's Lit usage elsewhere (no other element in fleet-lite-app manually patches the DOM outside of Lit's render cycle). If profiling later shows this is too slow at 1× speed (20 ticks/sec), the direct-DOM approach can be reintroduced.

- [x] **Step 7: Add the `render()` method**

Insert after the `endTs` getter:

```typescript

    override render() {
        const distFmt = formatDistance(this.trip.distanceKm ?? undefined, 1);
        const avgFmt = formatSpeed(this.trip.avgSpeedKph ?? undefined);
        const maxFmt = formatSpeed(this.trip.maxSpeedKph ?? undefined);
        const hasControls = !this.isSparse && !this.loading && !this.fetchError;

        return html`
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <div class="replay-header">
                    <div>
                        <span class="replay-title">Trip Replay</span>
                        <span class="replay-subtitle">
                            ${dayjs(this.trip.startTime).format('MMM D · HH:mm')} → ${this.endTs} · ${fmtDuration(this.trip.duration)}
                        </span>
                    </div>
                    <button class="close" @click=${this.dispatchClose}>
                        <span class="material-symbols-outlined">close</span>
                    </button>
                </div>

                <div class="map-wrapper">
                    ${this.loading
                        ? html`<div class="map-state">Loading GPS data…</div>`
                        : this.fetchError
                            ? html`<div class="map-state error">${this.fetchError}</div>`
                            : html`<div id="map"></div>`}
                    ${this.isSparse ? html`<div class="sparse-msg">GPS data sparse — showing start and end only</div>` : nothing}
                </div>

                <div class="stats-bar">
                    <div class="stat">
                        <div class="stat-label">Distance</div>
                        <div class="stat-value">${distFmt.value}<span class="unit">${distFmt.unit}</span></div>
                    </div>
                    <div class="stat">
                        <div class="stat-label">Duration</div>
                        <div class="stat-value">${fmtDuration(this.trip.duration)}</div>
                    </div>
                    <div class="stat">
                        <div class="stat-label">Avg speed</div>
                        <div class="stat-value">${avgFmt.value}<span class="unit">${avgFmt.unit}</span></div>
                    </div>
                    <div class="stat">
                        <div class="stat-label">Max speed</div>
                        <div class="stat-value">${maxFmt.value}<span class="unit">${maxFmt.unit}</span></div>
                    </div>
                </div>

                ${hasControls ? html`
                    <div class="controls">
                        <div class="progress-bar">
                            <div class="progress-track">
                                <div class="progress-fill" style="width:${this.progressPct}%"></div>
                            </div>
                            ${this.eventFlags.map((flag) => html`
                                <div class="event-tick" style="left:${flag.pct}%;--tick-color:${EVENT_COLORS[flag.name] ?? '#64748B'}">
                                    <div class="event-tick-tooltip">
                                        ${flag.name.replace(/^[^.]+\./, '').replace(/([A-Z])/g, ' $1').trim().toUpperCase()}
                                    </div>
                                </div>
                            `)}
                        </div>
                        <span class="time-display">${this.currentTs} / ${this.endTs}</span>
                        <button class="ctrl-btn primary" @click=${this.togglePlay} title=${this.isPlaying ? 'Pause' : 'Play'}>
                            <span class="material-symbols-outlined">${this.isPlaying ? 'pause' : 'play_arrow'}</span>
                        </button>
                        <button class="ctrl-btn" @click=${this.doReset} title="Reset">
                            <span class="material-symbols-outlined">replay</span>
                        </button>
                        <select class="speed-select" @change=${this.onSpeedChange}>
                            <option value="1" ?selected=${this.speedMultiplier === 1}>1×</option>
                            <option value="2" ?selected=${this.speedMultiplier === 2}>2×</option>
                            <option value="4" ?selected=${this.speedMultiplier === 4}>4×</option>
                        </select>
                    </div>
                ` : nothing}
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'trip-replay-modal': TripReplayModal;
    }
}
```

- [x] **Step 8: Type-check to verify it compiles**

Run: `cd web && npx tsc --noEmit`
Expected: no errors. Common issues to check if it fails:
- `material-symbols-outlined` usage: confirm this class is used elsewhere as a plain `<span>` (it is, e.g. `vehicle-details.ts:238`) — no import needed, it's a global font-icon class from `index.html`.
- `unsafeCSS`/`leafletCss` import: confirm this matches `fleet-overview.ts:4,235` exactly (`leaflet/dist/leaflet.css?inline` + `unsafeCSS(leafletCss)`).

- [x] **Step 9: Commit**

```bash
cd /Users/jamesli/DIMO/fleet-lite-app
git add web/src/elements/trip-replay-modal.ts
git commit -m "feat(web): add trip-replay-modal element with animated GPS playback"
```

---

### Task 5: Wire the Replay button into the Trips card

**Files:**
- Modify: `web/src/views/vehicle-details.ts`

- [ ] **Step 1: Import the new element and add `replayTrip` state**

At the top of `web/src/views/vehicle-details.ts`, add an import after the existing element/service imports (e.g. near line 9, after the `PrefsService` import):

```typescript
import '../elements/trip-replay-modal.ts';
```

Add a new state property after `@state() private tripsExpanded = false;` (line 36):

```typescript
    @state() private replayTrip: Trip | null = null;
```

- [ ] **Step 2: Add the Replay button to `renderTripRow`**

In `renderTripRow` (lines 231-263), add a Replay button to `.trip-stats`, shown only for completed trips with both timestamps. Replace the closing of the `.trip-stats` div — change:

```typescript
                    <span class="trip-stat ${trip.isOngoing ? 'ongoing' : ''}">
                        <span class="label">Duration</span>
                        <span class="value">${trip.isOngoing ? 'In progress' : this.tripDuration(trip.duration)}</span>
                    </span>
                </div>
            </div>
        `;
```

to:

```typescript
                    <span class="trip-stat ${trip.isOngoing ? 'ongoing' : ''}">
                        <span class="label">Duration</span>
                        <span class="value">${trip.isOngoing ? 'In progress' : this.tripDuration(trip.duration)}</span>
                    </span>
                </div>
                ${!trip.isOngoing && trip.endTime ? html`
                    <button class="trip-replay-btn" @click=${() => { this.replayTrip = trip; }}>
                        <span class="material-symbols-outlined">play_circle</span>
                        Replay
                    </button>
                ` : nothing}
            </div>
        `;
```

- [ ] **Step 3: Add `.trip-replay-btn` styling**

Find the `.trip-row` CSS rule in this file's `static styles` block (search for `.trip-row {`) and add a sibling rule directly after it:

```typescript
            .trip-replay-btn {
                display: flex;
                align-items: center;
                gap: 6px;
                align-self: flex-start;
                margin-top: 8px;
                padding: 6px 14px;
                background: transparent;
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                color: var(--on-surface);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                cursor: pointer;
            }
            .trip-replay-btn:hover { background: var(--surface-container-high); }
            .trip-replay-btn .material-symbols-outlined { font-size: 16px; }
```

- [ ] **Step 4: Mount the modal conditionally in `render()`**

Find the end of the `render()` method's returned template in `vehicle-details.ts` (the closing of the outermost `html\`...\`` block, just before its final backtick and the method's closing brace). Add the conditional mount immediately before that closing backtick:

```typescript
            ${this.replayTrip ? html`
                <trip-replay-modal
                    .trip=${this.replayTrip}
                    .tokenId=${Number(this.tokenId)}
                    @close=${() => { this.replayTrip = null; }}>
                </trip-replay-modal>
            ` : nothing}
```

- [ ] **Step 5: Reset `replayTrip` on reload**

In `loadAll()`, in the state-reset block at the top (lines 51-57, alongside `this.trips = []` and `this.tripsExpanded = false`), add:

```typescript
        this.replayTrip = null;
```

- [ ] **Step 6: Type-check to verify it compiles**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
cd /Users/jamesli/DIMO/fleet-lite-app
git add web/src/views/vehicle-details.ts
git commit -m "feat(web): add Replay button to trip rows, wire up trip-replay-modal"
```

---

### Task 6: Manual end-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Start the dev stack**

Run: `make dev` (from the repo root) — brings up frontend (Vite, port 3009) and backend (Go, port 8084) together. Wait for both to report ready.

- [ ] **Step 2: Open a vehicle details page with trip history**

In a browser, navigate to `https://local-fleet-lite.dimo.org:3009`, sign in, open a vehicle that has completed trips in its Trips card (use one with recent driving activity — check the memory/observations from the Jun 6–7 trips work for a known-good token ID, or pick any vehicle showing trips with non-"Ongoing" end times).

- [ ] **Step 3: Open the replay modal and verify playback**

Click "Replay" on a completed trip row. Verify:
- The modal opens with a dark-themed map (matching the fleet overview map's tile style), green start marker, red end marker, and a faded full-route polyline
- The stats bar shows distance/duration/avg speed/max speed matching the trip row's values
- Clicking ▶ animates a blue marker moving along the route, drawing a solid polyline behind it, advancing the progress bar and time display
- Changing the speed selector (1×/2×/4×) visibly changes playback speed
- Clicking ↺ resets the marker to the start and the progress bar to 0%
- If the trip has any harsh-braking/cornering/acceleration events, color-coded ticks appear on the progress bar with hover tooltips
- Pressing Escape or clicking the close (×) button closes the modal

- [ ] **Step 4: Verify the sparse-data fallback (if a short/low-GPS trip is available)**

Open replay on a very short trip (under ~1 minute, likely to have fewer than 2 waypoints at 30s sampling). Verify the modal shows a "GPS data sparse" message with just start/end markers and a dashed connecting line, and no playback controls.

- [ ] **Step 5: Verify graceful handling without DIMO permissions**

If you have access to a vehicle/tenant combination without telemetry permissions granted, open replay on one of its trips and verify the modal shows the "Grant DIMO permissions…" message rather than erroring.

- [ ] **Step 6: Report results**

Note any visual or behavioral issues found. If everything works as expected, the feature is complete — no further commits needed for this task.

---

## Self-Review Notes

- **Spec coverage:** Task 1–2 cover the backend endpoint/types/service (spec §Backend); Task 3 covers frontend types/service (spec §Frontend types/service); Task 4 covers the modal component including map, animation, controls, event markers, and fallback (spec §Frontend element); Task 5 covers the Replay button and integration (spec §Integration); error handling (permissions, sparse data) is covered in Task 4 Steps 4–5 and verified in Task 6 Steps 4–5.
- **Type consistency:** `TripWaypoint`/`TripEvent`/`TripRouteResponse` are defined identically in Go (Task 1) and TypeScript (Task 3); `tripRoute(tokenId, from, to)` signature matches between service method (Task 3) and modal usage (Task 4 Step 4); `replayTrip: Trip | null` matches the existing `Trip` type from `types/telemetry.ts` used throughout `vehicle-details.ts`.
- **No placeholders:** every step contains complete, concrete code — no "add error handling" or "similar to Task N" references.
