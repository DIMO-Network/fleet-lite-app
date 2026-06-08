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
	LatestLocation(tenant models.Tenant, tokenID uint64) (*VehicleLocation, error)
	TimeSeries(tenant models.Tenant, tokenID uint64, signal, from, to, interval string) ([]TimeSeriesBucket, error)
	// FleetLocations checks per-vehicle JWT availability to determine which
	// vehicles the tenant's dev license has SACD permissions for, then fetches
	// coordinates for those vehicles in a single aliased GraphQL request.
	// Vehicles where JWT exchange fails are returned in NoPermissions.
	FleetLocations(tenant models.Tenant, tokenIDs []uint64) (FleetLocationsResult, error)
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
