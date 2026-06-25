package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
)

// fleetLocationsConcurrency bounds the per-vehicle fan-out (JWT exchange +
// telemetry query each). High enough to collapse the serial latency, low
// enough to be polite to token-exchange and telemetry-api.
const fleetLocationsConcurrency = 10

// fleetLocationsCacheTTL is how long a vehicle's resolved location outcome is
// served from memory. Repeat map loads (and other users on the same tenant)
// within the window skip that vehicle's fan-out. The map's manual refresh
// button bypasses reads via force (fresh outcomes are still written back).
const fleetLocationsCacheTTL = 45 * time.Second

// locCacheEntry is one vehicle's resolved outcome. Cached per vehicle (not
// per tenant snapshot) so the frontend's paged subset requests compose with
// full-fleet requests instead of each maintaining a separate snapshot.
// Semantics: noPerm -> NoPermissions; coords set -> Locations; neither ->
// queried fine but no location data. Transient query errors are not cached.
type locCacheEntry struct {
	coords *LocationCoords
	noPerm bool
}

func locCacheKey(tenantID string, tokenID uint64) string {
	return fmt.Sprintf("%s:%d", tenantID, tokenID)
}

// TelemetryAPIService wraps telemetry-api.dimo.zone/query (GraphQL).
//
// Auth: vehicle JWT obtained via DimoAuthProvider.GetVehicleJWT with the
// numeric privilege set [1, 3, 4, 5] (NonLocationHistory, CurrentLocation,
// VINCredential, RawData).  Same SACD-grant constraint as glovebox: the dev
// license must have permissions on the vehicle.
// LocationCoords is the decoded value of a currentLocationCoordinates signal.
type LocationCoords struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// TripWaypoint is one GPS fix sampled at a fixed interval across a trip's
// window, carrying the timestamp so playback can pace to real time.
type TripWaypoint struct {
	Timestamp string  `json:"timestamp"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
}

// TripEvent is a discrete behavior event (e.g. harsh braking) within a trip's
// window, placed on the replay timeline by its timestamp.
type TripEvent struct {
	Timestamp  string `json:"timestamp"`
	Name       string `json:"name"`
	DurationNs int64  `json:"durationNs"`
}

// GeoSample is one interval-bucketed telemetry fix used for geofence detection:
// a timestamped coordinate plus the bucket's max speed (nil when the vehicle
// reported no speed in that bucket). Coarser than RoutePoints but timestamped,
// and carries speed in the same query — see GeofenceSamples.
type GeoSample struct {
	Time     time.Time
	Lat      float64
	Lng      float64
	SpeedKph *float64
}

// FleetLocationsResult separates vehicles with accessible location data from
// those where the developer license lacks SACD permissions.
type FleetLocationsResult struct {
	Locations     map[uint64]LocationCoords
	NoPermissions []uint64
}

type TelemetryAPIService interface {
	Latest(tenant models.Tenant, tokenID uint64, signals []string) (map[string]SignalLatest, error)
	LatestLocation(tenant models.Tenant, tokenID uint64) (*VehicleLocation, error)
	TimeSeries(tenant models.Tenant, tokenID uint64, signal, from, to, interval string) ([]TimeSeriesBucket, error)
	// Segments queries telemetry-api's trip detection (`segments`) for one
	// vehicle. mechanism is a telemetry-api enum: "ignitionDetection" or
	// "frequencyAnalysis" (aftermarket devices only support the latter).
	Segments(tenant models.Tenant, tokenID uint64, from, to, mechanism string) ([]Segment, error)
	// RoutePoints samples currentLocationCoordinates at a 3s interval over a
	// trip's time window, returning the polyline points in order.
	RoutePoints(tenant models.Tenant, tokenID uint64, from, to string) ([]LocationCoords, error)
	// TripReplay samples timestamped GPS waypoints (coarser than RoutePoints)
	// plus discrete behavior events over a trip's window, for animated replay.
	TripReplay(tenant models.Tenant, tokenID uint64, from, to string) ([]TripWaypoint, []TripEvent, error)
	// GeofenceSamples returns interval-bucketed {timestamp, coordinate, max
	// speed} fixes over a window, ordered by time — the input to geofence pass
	// detection. interval is a telemetry-api duration (e.g. "30s"); coordinates
	// and speed come from one bucketed `signals` query.
	GeofenceSamples(tenant models.Tenant, tokenID uint64, from, to, interval string) ([]GeoSample, error)
	// FleetLocations checks per-vehicle JWT availability to determine which
	// vehicles the tenant's dev license has SACD permissions for, then fetches
	// each permitted vehicle's coordinates with its own JWT (telemetry-api
	// rejects developer JWTs, so one request per vehicle is unavoidable).
	// Requests run concurrently with a bounded worker pool. Vehicles where
	// JWT exchange fails are returned in NoPermissions. Results are cached
	// per tenant for fleetLocationsCacheTTL; force bypasses the cache.
	FleetLocations(ctx context.Context, tenant models.Tenant, tokenIDs []uint64, force bool) (FleetLocationsResult, error)
}

// SegmentPoint is one end of a detected trip: a GPS fix plus its timestamp.
type SegmentPoint struct {
	Value struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"value"`
	Timestamp string `json:"timestamp"`
}

// SegmentSignal is one requested aggregation over the trip window (e.g.
// odometer FIRST/LAST for distance, speed AVG/MAX).
type SegmentSignal struct {
	Name  string  `json:"name"`
	Agg   string  `json:"agg"`
	Value float64 `json:"value"`
}

// Segment is one detected trip from telemetry-api's `segments` query — the
// same shape the b2b fleet manager consumes on its details screen.
type Segment struct {
	Start     SegmentPoint    `json:"start"`
	End       SegmentPoint    `json:"end"`
	IsOngoing bool            `json:"isOngoing"`
	Signals   []SegmentSignal `json:"signals"`
}

// VehicleLocation is a vehicle's latest GPS fix from telemetry-api's
// currentLocationCoordinates signal (nested value, unlike scalar signals).
type VehicleLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timestamp string  `json:"timestamp"`
}

// SignalLatest mirrors telemetry-api's `{value, timestamp}` shape for a signal.
type SignalLatest struct {
	Value     interface{} `json:"value"`
	Timestamp string      `json:"timestamp"`
}

// TimeSeriesBucket is one aggregation point. Numeric signals aggregate as
// min/max/avg/last; we return all four so the frontend can pick per-card.
type TimeSeriesBucket struct {
	Timestamp string  `json:"timestamp"`
	Min       float64 `json:"min"`
	Max       float64 `json:"max"`
	Avg       float64 `json:"avg"`
	Last      float64 `json:"last"`
}

type telemetryAPIService struct {
	logger       zerolog.Logger
	authProvider *gateway.DimoAuthProvider
	endpoint     string
	locCache     *cache.Cache // "tenantID:tokenID" -> locCacheEntry
}

func NewTelemetryAPIService(logger zerolog.Logger, settings *config.Settings, authProvider *gateway.DimoAuthProvider) TelemetryAPIService {
	return &telemetryAPIService{
		logger:       logger,
		authProvider: authProvider,
		endpoint:     settings.TelemetryAPIURL.String(),
		locCache:     cache.New(fleetLocationsCacheTTL, 2*fleetLocationsCacheTTL),
	}
}

// Latest queries `signalsLatest(tokenId)` for the given signal names.
// Returns a map keyed by the camelCase signal name as it appears in the
// GraphQL schema (same as `signals` arg). Missing signals are omitted from
// the map (silent — not all vehicles report every signal).
func (t *telemetryAPIService) Latest(tenant models.Tenant, tokenID uint64, signals []string) (map[string]SignalLatest, error) {
	if len(signals) == 0 {
		return map[string]SignalLatest{}, nil
	}

	// Build the per-signal selection. Each signal returns {value, timestamp}.
	var sb strings.Builder
	fmt.Fprintf(&sb, "query { signalsLatest(tokenId: %d) {", tokenID)
	for _, s := range signals {
		fmt.Fprintf(&sb, " %s { value timestamp }", s)
	}
	sb.WriteString(" } }")

	raw, err := t.query(tenant, tokenID, sb.String())
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			SignalsLatest map[string]SignalLatest `json:"signalsLatest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse signalsLatest: %w", err)
	}
	return resp.Data.SignalsLatest, nil
}

// LatestLocation queries `currentLocationCoordinates` for one vehicle. Returns
// nil (no error) when the vehicle has never reported a location. The signal's
// value is nested ({latitude, longitude}) so it can't go through Latest's
// scalar SignalLatest shape.
func (t *telemetryAPIService) LatestLocation(tenant models.Tenant, tokenID uint64) (*VehicleLocation, error) {
	q := fmt.Sprintf("query { signalsLatest(tokenId: %d) { currentLocationCoordinates { value { latitude longitude } timestamp } } }", tokenID)

	raw, err := t.query(tenant, tokenID, q)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			SignalsLatest struct {
				CurrentLocationCoordinates *struct {
					Value struct {
						Latitude  float64 `json:"latitude"`
						Longitude float64 `json:"longitude"`
					} `json:"value"`
					Timestamp string `json:"timestamp"`
				} `json:"currentLocationCoordinates"`
			} `json:"signalsLatest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse currentLocationCoordinates: %w", err)
	}

	loc := resp.Data.SignalsLatest.CurrentLocationCoordinates
	if loc == nil {
		return nil, nil
	}
	return &VehicleLocation{
		Latitude:  loc.Value.Latitude,
		Longitude: loc.Value.Longitude,
		Timestamp: loc.Timestamp,
	}, nil
}

// TimeSeries queries `signals(tokenId, from, to, interval)` for one signal
// and returns its min/max/avg/last buckets. Interval is a duration string
// the telemetry-api recognizes (e.g. "1d", "1h", "15m").
//
// The telemetry-api schema requires an `agg` argument per signal and returns a
// scalar Float — we alias the same field four times to get all aggregations.
func (t *telemetryAPIService) TimeSeries(tenant models.Tenant, tokenID uint64, signal, from, to, interval string) ([]TimeSeriesBucket, error) {
	q := fmt.Sprintf(`query {
		signals(tokenId: %d, from: %q, to: %q, interval: %q) {
			timestamp
			min: %s(agg: MIN)
			max: %s(agg: MAX)
			avg: %s(agg: AVG)
			last: %s(agg: LAST)
		}
	}`, tokenID, from, to, interval, signal, signal, signal, signal)

	raw, err := t.query(tenant, tokenID, q)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			Signals []map[string]json.RawMessage `json:"signals"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse signals time series: %w", err)
	}

	buckets := make([]TimeSeriesBucket, 0, len(resp.Data.Signals))
	for _, bucket := range resp.Data.Signals {
		b := TimeSeriesBucket{}
		if ts, ok := bucket["timestamp"]; ok {
			_ = json.Unmarshal(ts, &b.Timestamp)
		}
		for ptr, key := range map[*float64]string{&b.Min: "min", &b.Max: "max", &b.Avg: "avg", &b.Last: "last"} {
			if v, ok := bucket[key]; ok {
				_ = json.Unmarshal(v, ptr)
			}
		}
		buckets = append(buckets, b)
	}
	return buckets, nil
}

// ValidSegmentMechanisms is the set of telemetry-api segmentation strategies
// accepted by the segments query. Each is a GraphQL enum literal interpolated
// unquoted into the query, so the allowlist doubles as injection protection.
var ValidSegmentMechanisms = []string{
	"ignitionDetection",
	"frequencyAnalysis",
	"changePointDetection",
	"idling",
	"refuel",
	"recharge",
}

// IsValidSegmentMechanism reports whether s is one of ValidSegmentMechanisms.
func IsValidSegmentMechanism(s string) bool {
	for _, m := range ValidSegmentMechanisms {
		if m == s {
			return true
		}
	}
	return false
}

// Segments queries trip detection for one vehicle. Query shape mirrors the
// b2b fleet manager's details screen: odometer FIRST/LAST (distance) and
// speed AVG/MAX per segment. mechanism is interpolated unquoted — it is a
// GraphQL enum — and restricted to the known allowlist to keep the query
// well-formed and injection-safe.
func (t *telemetryAPIService) Segments(tenant models.Tenant, tokenID uint64, from, to, mechanism string) ([]Segment, error) {
	if !IsValidSegmentMechanism(mechanism) {
		return nil, fmt.Errorf("unknown segments mechanism %q", mechanism)
	}
	q := fmt.Sprintf(`query {
		segments(
			tokenId: %d
			from: %q
			to: %q
			mechanism: %s
			limit: 60
			signalRequests: [
				{ name: "powertrainTransmissionTravelledDistance", agg: FIRST }
				{ name: "powertrainTransmissionTravelledDistance", agg: LAST }
				{ name: "speed", agg: AVG }
				{ name: "speed", agg: MAX }
			]
		) {
			start { value { latitude longitude } timestamp }
			end { value { latitude longitude } timestamp }
			isOngoing
			signals { name agg value }
		}
	}`, tokenID, from, to, mechanism)

	raw, err := t.query(tenant, tokenID, q)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			Segments []Segment `json:"segments"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse segments: %w", err)
	}
	return resp.Data.Segments, nil
}

// RoutePoints samples the vehicle's location every 3s across a trip window —
// the same query the b2b app uses to draw a trip's route polyline.
func (t *telemetryAPIService) RoutePoints(tenant models.Tenant, tokenID uint64, from, to string) ([]LocationCoords, error) {
	q := fmt.Sprintf(`query {
		signals(tokenId: %d, interval: "3s", from: %q, to: %q) {
			currentLocationCoordinates(agg: FIRST) { latitude longitude }
		}
	}`, tokenID, from, to)

	raw, err := t.query(tenant, tokenID, q)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			Signals []struct {
				CurrentLocationCoordinates *struct {
					Latitude  float64 `json:"latitude"`
					Longitude float64 `json:"longitude"`
				} `json:"currentLocationCoordinates"`
			} `json:"signals"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse route points: %w", err)
	}

	points := make([]LocationCoords, 0, len(resp.Data.Signals))
	for _, s := range resp.Data.Signals {
		c := s.CurrentLocationCoordinates
		if c == nil || (c.Latitude == 0 && c.Longitude == 0) {
			continue
		}
		points = append(points, LocationCoords{Lat: c.Latitude, Lon: c.Longitude})
	}
	return points, nil
}

// TripReplay fetches timestamped GPS waypoints (sampled at a 30s interval —
// coarser than RoutePoints' 3s, since playback animates between fixes rather
// than drawing a static polyline) plus the trip's behavior events, for the
// animated trip-replay modal.
func (t *telemetryAPIService) TripReplay(tenant models.Tenant, tokenID uint64, from, to string) ([]TripWaypoint, []TripEvent, error) {
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
				Timestamp                  string `json:"timestamp"`
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
		return nil, nil, fmt.Errorf("parse trip replay: %w", err)
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
		Int("waypoints", len(waypoints)).Int("events", len(events)).Msg("telemetry trip replay fetched")

	return waypoints, events, nil
}

// GeofenceSamples fetches interval-bucketed coordinate + max-speed fixes over a
// window for geofence pass detection. Coordinates use LAST (the bucket's final
// position) and speed uses MAX (the worst-case for speed-limit checks) — both
// in one `signals` query, so adding speed costs a field, not a round-trip.
// Buckets without a coordinate are dropped; speed is nil when absent.
func (t *telemetryAPIService) GeofenceSamples(tenant models.Tenant, tokenID uint64, from, to, interval string) ([]GeoSample, error) {
	if interval == "" {
		interval = "30s"
	}
	q := fmt.Sprintf(`query {
		samples: signals(tokenId: %d, from: %q, to: %q, interval: %q) {
			timestamp
			currentLocationCoordinates(agg: LAST) { latitude longitude }
			speed(agg: MAX)
		}
	}`, tokenID, from, to, interval)

	raw, err := t.query(tenant, tokenID, q)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			Samples []struct {
				Timestamp                  string `json:"timestamp"`
				CurrentLocationCoordinates *struct {
					Latitude  float64 `json:"latitude"`
					Longitude float64 `json:"longitude"`
				} `json:"currentLocationCoordinates"`
				Speed *float64 `json:"speed"`
			} `json:"samples"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse geofence samples: %w", err)
	}

	out := make([]GeoSample, 0, len(resp.Data.Samples))
	for _, s := range resp.Data.Samples {
		if s.CurrentLocationCoordinates == nil {
			continue
		}
		ts, perr := time.Parse(time.RFC3339, s.Timestamp)
		if perr != nil {
			continue
		}
		out = append(out, GeoSample{
			Time:     ts,
			Lat:      s.CurrentLocationCoordinates.Latitude,
			Lng:      s.CurrentLocationCoordinates.Longitude,
			SpeedKph: s.Speed,
		})
	}
	// signals returns buckets in ascending time, but detection relies on order —
	// sort defensively in case the API ever changes.
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })

	t.logger.Info().Uint64("tokenID", tokenID).Str("from", from).Str("to", to).
		Int("samples", len(out)).Msg("telemetry geofence samples fetched")

	return out, nil
}

// FleetLocations checks per-vehicle JWT availability (definitive SACD check),
// then fetches coordinates for each permitted vehicle using its own JWT.
// The developer JWT is intentionally not used here — it is rejected by the
// telemetry API as invalid in most deployment configurations. That auth
// constraint forces one request per vehicle, so the fan-out runs through a
// bounded worker pool instead of serially (a large fleet done one-by-one
// blows past the ingress timeout).
func (t *telemetryAPIService) FleetLocations(ctx context.Context, tenant models.Tenant, tokenIDs []uint64, force bool) (FleetLocationsResult, error) {
	if len(tokenIDs) == 0 {
		return FleetLocationsResult{Locations: map[uint64]LocationCoords{}}, nil
	}

	var (
		mu      sync.Mutex
		locs    = make(map[uint64]LocationCoords, len(tokenIDs))
		noPerms []uint64
	)

	// Resolve from the per-vehicle cache first; only cache misses fan out.
	// force skips reads but fresh outcomes still refresh the cache below.
	remaining := tokenIDs
	if !force {
		remaining = make([]uint64, 0, len(tokenIDs))
		for _, id := range tokenIDs {
			cached, found := t.locCache.Get(locCacheKey(tenant.ID, id))
			if !found {
				remaining = append(remaining, id)
				continue
			}
			entry := cached.(locCacheEntry)
			switch {
			case entry.noPerm:
				noPerms = append(noPerms, id)
			case entry.coords != nil:
				locs[id] = *entry.coords
			}
		}
		if len(remaining) == 0 {
			return FleetLocationsResult{Locations: locs, NoPermissions: noPerms}, nil
		}
	}

	// Warm the developer JWT once so concurrent vehicle exchanges below all
	// hit the cache instead of racing N simultaneous dev-JWT exchanges.
	if _, err := t.authProvider.GetDeveloperJWT(tenant); err != nil {
		// Every vehicle exchange would fail the same way — same outcome as
		// the serial version (all vehicles land in NoPermissions), minus N calls.
		t.logger.Warn().Err(err).Msg("fleet locations: developer JWT unavailable")
		return FleetLocationsResult{Locations: map[uint64]LocationCoords{}, NoPermissions: tokenIDs}, nil
	}

	// NOTE: keep the errgroup-derived context in its own variable. errgroup
	// cancels the derived context when Wait returns — even on success — so
	// checking the parent's liveness must use `ctx`, never `gctx`. (Shadowing
	// ctx here once turned every successful fan-out into a 502.)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fleetLocationsConcurrency)

	for _, id := range remaining {
		if gctx.Err() != nil {
			break // caller gone — stop scheduling work
		}
		g.Go(func() error {
			jwt, err := t.authProvider.GetVehicleJWT(tenant, id)
			if err != nil {
				t.locCache.Set(locCacheKey(tenant.ID, id), locCacheEntry{noPerm: true}, fleetLocationsCacheTTL)
				mu.Lock()
				noPerms = append(noPerms, id)
				mu.Unlock()
				return nil
			}

			q := fmt.Sprintf(`query { signalsLatest(tokenId: %d) { currentLocationCoordinates { value { latitude longitude } } } }`, id)
			raw, err := t.doQuery(gctx, jwt, q)
			if err != nil {
				// JWT worked but query failed (e.g. telemetry API hiccup) — skip silently.
				t.logger.Warn().Uint64("tokenId", id).Err(err).Msg("fleet locations: skip vehicle query error")
				return nil
			}

			var resp struct {
				Data struct {
					SignalsLatest struct {
						CurrentLocationCoordinates *struct {
							Value *struct {
								Latitude  float64 `json:"latitude"`
								Longitude float64 `json:"longitude"`
							} `json:"value"`
						} `json:"currentLocationCoordinates"`
					} `json:"signalsLatest"`
				} `json:"data"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return nil
			}
			coords := resp.Data.SignalsLatest.CurrentLocationCoordinates
			if coords == nil || coords.Value == nil {
				// Queried fine, just no location data — cache so the next
				// window doesn't re-query a vehicle that reports nothing.
				t.locCache.Set(locCacheKey(tenant.ID, id), locCacheEntry{}, fleetLocationsCacheTTL)
				return nil
			}
			c := LocationCoords{Lat: coords.Value.Latitude, Lon: coords.Value.Longitude}
			t.locCache.Set(locCacheKey(tenant.ID, id), locCacheEntry{coords: &c}, fleetLocationsCacheTTL)
			mu.Lock()
			locs[id] = c
			mu.Unlock()
			return nil
		})
	}

	// Workers never return errors (per-vehicle failures degrade to skips, same
	// as the serial version), so Wait only reflects context cancellation.
	if err := g.Wait(); err != nil {
		return FleetLocationsResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return FleetLocationsResult{}, err
	}

	return FleetLocationsResult{Locations: locs, NoPermissions: noPerms}, nil
}

func (t *telemetryAPIService) query(tenant models.Tenant, tokenID uint64, gql string) ([]byte, error) {
	jwt, err := t.authProvider.GetVehicleJWT(tenant, tokenID)
	if err != nil {
		return nil, fmt.Errorf("vehicle JWT: %w", err)
	}
	return t.doQuery(context.Background(), jwt, gql)
}

func (t *telemetryAPIService) doQuery(ctx context.Context, jwt, gql string) ([]byte, error) {
	payload, err := json.Marshal(map[string]string{"query": gql})
	if err != nil {
		return nil, fmt.Errorf("marshal gql: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", t.endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("build telemetry request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telemetry request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read telemetry response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("telemetry status %d: %s", resp.StatusCode, string(body))
	}

	var probe struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &probe); err == nil && len(probe.Errors) > 0 {
		return nil, fmt.Errorf("telemetry gql error: %s", probe.Errors[0].Message)
	}
	return body, nil
}
