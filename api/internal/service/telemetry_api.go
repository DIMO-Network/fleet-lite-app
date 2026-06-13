package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/rs/zerolog"
)

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

// FleetLocationsResult separates vehicles with accessible location data from
// those where the developer license lacks SACD permissions.
type FleetLocationsResult struct {
	Locations     map[uint64]LocationCoords
	NoPermissions []uint64
}

type TelemetryAPIService interface {
	Latest(tenant models.Tenant, tokenID uint64, signals []string) (map[string]SignalLatest, error)
	TimeSeries(tenant models.Tenant, tokenID uint64, signal, from, to, interval string) ([]TimeSeriesBucket, error)
	// FleetLocations checks per-vehicle JWT availability to determine which
	// vehicles the tenant's dev license has SACD permissions for, then fetches
	// coordinates for those vehicles in a single aliased GraphQL request.
	// Vehicles where JWT exchange fails are returned in NoPermissions.
	FleetLocations(tenant models.Tenant, tokenIDs []uint64) (FleetLocationsResult, error)
	// Trips queries detected driving segments (`segments`) for a vehicle
	// within [from, to] using the given detection mechanism. The telemetry-api
	// caps this range at 31 days.
	Trips(tenant models.Tenant, tokenID uint64, from, to string, mechanism DetectionMechanism) ([]Trip, error)
	// TripRoute fetches GPS waypoints (sampled every 30s) and behavior events
	// for a trip's [from, to] window, used to animate route playback.
	TripRoute(tenant models.Tenant, tokenID uint64, from, to string) ([]TripWaypoint, []TripEvent, error)
}

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
	MechanismRecharge             DetectionMechanism = "recharge"
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

// Trip is one detected driving segment for a vehicle, derived from
// telemetry-api's `segments` query using ignition-based detection — the
// mechanism that best matches the everyday notion of "started driving" /
// "stopped driving".
type Trip struct {
	StartTime     string          `json:"startTime"`
	StartLocation *LocationCoords `json:"startLocation"`
	EndTime       *string         `json:"endTime"`
	EndLocation   *LocationCoords `json:"endLocation"`
	Duration      int             `json:"duration"` // seconds
	IsOngoing     bool            `json:"isOngoing"`
	DistanceKm    *float64        `json:"distanceKm"`
	AvgSpeedKph   *float64        `json:"avgSpeedKph"`
	MaxSpeedKph   *float64        `json:"maxSpeedKph"`
}

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
}

func NewTelemetryAPIService(logger zerolog.Logger, settings *config.Settings, authProvider *gateway.DimoAuthProvider) TelemetryAPIService {
	return &telemetryAPIService{
		logger:       logger,
		authProvider: authProvider,
		endpoint:     settings.TelemetryAPIURL.String(),
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

// TimeSeries queries `signals(tokenId, from, to, interval)` for one signal
// and returns its min/max/avg/last buckets. Interval is a Go duration string
// (e.g. "24h", "1h", "15m") — the telemetry-api parses it with time.ParseDuration,
// which has no "d" (day) unit.
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

// FleetLocations checks per-vehicle JWT availability (definitive SACD check),
// then fetches coordinates for each permitted vehicle using its own JWT.
// The developer JWT is intentionally not used here — it is rejected by the
// telemetry API as invalid in most deployment configurations.
func (t *telemetryAPIService) FleetLocations(tenant models.Tenant, tokenIDs []uint64) (FleetLocationsResult, error) {
	if len(tokenIDs) == 0 {
		return FleetLocationsResult{Locations: map[uint64]LocationCoords{}}, nil
	}

	locs := make(map[uint64]LocationCoords, len(tokenIDs))
	var noPerms []uint64

	for _, id := range tokenIDs {
		jwt, err := t.authProvider.GetVehicleJWT(tenant, id)
		if err != nil {
			noPerms = append(noPerms, id)
			continue
		}

		q := fmt.Sprintf(`query { signalsLatest(tokenId: %d) { currentLocationCoordinates { value { latitude longitude } } } }`, id)
		raw, err := t.doQuery(jwt, q)
		if err != nil {
			// JWT worked but query failed (e.g. telemetry API hiccup) — skip silently.
			t.logger.Warn().Uint64("tokenId", id).Err(err).Msg("fleet locations: skip vehicle query error")
			continue
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
			continue
		}
		coords := resp.Data.SignalsLatest.CurrentLocationCoordinates
		if coords == nil || coords.Value == nil {
			continue
		}
		locs[id] = LocationCoords{Lat: coords.Value.Latitude, Lon: coords.Value.Longitude}
	}

	return FleetLocationsResult{Locations: locs, NoPermissions: noPerms}, nil
}

// Trips queries `segments(tokenId, from, to, mechanism)` and maps each
// segment to a flat Trip, requesting the signal aggregations needed to derive
// distance and avg/max speed. `end` is null for a trip still in progress
// (isOngoing: true).
func (t *telemetryAPIService) Trips(tenant models.Tenant, tokenID uint64, from, to string, mechanism DetectionMechanism) ([]Trip, error) {
	if !IsValidDetectionMechanism(string(mechanism)) {
		return nil, fmt.Errorf("invalid detection mechanism: %q", mechanism)
	}
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

	raw, err := t.query(tenant, tokenID, q)
	if err != nil {
		return nil, err
	}

	type signalLocation struct {
		Timestamp string `json:"timestamp"`
		Value     *struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"value"`
	}
	type signalAgg struct {
		Name  string  `json:"name"`
		Agg   string  `json:"agg"`
		Value float64 `json:"value"`
	}
	var resp struct {
		Data struct {
			Segments []struct {
				Start     signalLocation  `json:"start"`
				End       *signalLocation `json:"end"`
				Duration  int             `json:"duration"`
				IsOngoing bool            `json:"isOngoing"`
				Signals   []signalAgg     `json:"signals"`
			} `json:"segments"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse segments: %w", err)
	}
	t.logger.Info().Uint64("tokenID", tokenID).Str("from", from).Str("to", to).
		Str("mechanism", string(mechanism)).Int("segments", len(resp.Data.Segments)).Msg("telemetry segments fetched")

	toCoords := func(loc signalLocation) *LocationCoords {
		if loc.Value == nil {
			return nil
		}
		return &LocationCoords{Lat: loc.Value.Latitude, Lon: loc.Value.Longitude}
	}

	findAgg := func(signals []signalAgg, name, agg string) *float64 {
		for _, s := range signals {
			if s.Name == name && s.Agg == agg {
				v := s.Value
				return &v
			}
		}
		return nil
	}

	trips := make([]Trip, 0, len(resp.Data.Segments))
	for _, seg := range resp.Data.Segments {
		trip := Trip{
			StartTime:     seg.Start.Timestamp,
			StartLocation: toCoords(seg.Start),
			Duration:      seg.Duration,
			IsOngoing:     seg.IsOngoing,
			AvgSpeedKph:   findAgg(seg.Signals, "speed", "AVG"),
			MaxSpeedKph:   findAgg(seg.Signals, "speed", "MAX"),
		}
		if first, last := findAgg(seg.Signals, "powertrainTransmissionTravelledDistance", "FIRST"),
			findAgg(seg.Signals, "powertrainTransmissionTravelledDistance", "LAST"); first != nil && last != nil {
			if delta := *last - *first; delta >= 0 {
				trip.DistanceKm = &delta
			}
		}
		if seg.End != nil {
			endTime := seg.End.Timestamp
			trip.EndTime = &endTime
			trip.EndLocation = toCoords(*seg.End)
		}
		trips = append(trips, trip)
	}
	return trips, nil
}

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

func (t *telemetryAPIService) query(tenant models.Tenant, tokenID uint64, gql string) ([]byte, error) {
	jwt, err := t.authProvider.GetVehicleJWT(tenant, tokenID)
	if err != nil {
		return nil, fmt.Errorf("vehicle JWT: %w", err)
	}
	return t.doQuery(jwt, gql)
}

func (t *telemetryAPIService) doQuery(jwt, gql string) ([]byte, error) {
	payload, err := json.Marshal(map[string]string{"query": gql})
	if err != nil {
		return nil, fmt.Errorf("marshal gql: %w", err)
	}
	req, err := http.NewRequest("POST", t.endpoint, bytes.NewBuffer(payload))
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
