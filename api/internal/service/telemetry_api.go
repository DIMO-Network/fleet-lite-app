package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
)

// fleetLocationsConcurrency bounds the per-vehicle fan-out (JWT exchange +
// telemetry query each). High enough to collapse the serial latency, low
// enough to be polite to token-exchange and telemetry-api.
const fleetLocationsConcurrency = 10

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
	// each permitted vehicle's coordinates with its own JWT (telemetry-api
	// rejects developer JWTs, so one request per vehicle is unavoidable).
	// Requests run concurrently with a bounded worker pool. Vehicles where
	// JWT exchange fails are returned in NoPermissions.
	FleetLocations(ctx context.Context, tenant models.Tenant, tokenIDs []uint64) (FleetLocationsResult, error)
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
// telemetry API as invalid in most deployment configurations. That auth
// constraint forces one request per vehicle, so the fan-out runs through a
// bounded worker pool instead of serially (a large fleet done one-by-one
// blows past the ingress timeout).
func (t *telemetryAPIService) FleetLocations(ctx context.Context, tenant models.Tenant, tokenIDs []uint64) (FleetLocationsResult, error) {
	if len(tokenIDs) == 0 {
		return FleetLocationsResult{Locations: map[uint64]LocationCoords{}}, nil
	}

	// Warm the developer JWT once so concurrent vehicle exchanges below all
	// hit the cache instead of racing N simultaneous dev-JWT exchanges.
	if _, err := t.authProvider.GetDeveloperJWT(tenant); err != nil {
		// Every vehicle exchange would fail the same way — same outcome as
		// the serial version (all vehicles land in NoPermissions), minus N calls.
		t.logger.Warn().Err(err).Msg("fleet locations: developer JWT unavailable")
		return FleetLocationsResult{Locations: map[uint64]LocationCoords{}, NoPermissions: tokenIDs}, nil
	}

	var (
		mu      sync.Mutex
		locs    = make(map[uint64]LocationCoords, len(tokenIDs))
		noPerms []uint64
	)

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(fleetLocationsConcurrency)

	for _, id := range tokenIDs {
		if ctx.Err() != nil {
			break // caller gone — stop scheduling work
		}
		g.Go(func() error {
			jwt, err := t.authProvider.GetVehicleJWT(tenant, id)
			if err != nil {
				mu.Lock()
				noPerms = append(noPerms, id)
				mu.Unlock()
				return nil
			}

			q := fmt.Sprintf(`query { signalsLatest(tokenId: %d) { currentLocationCoordinates { value { latitude longitude } } } }`, id)
			raw, err := t.doQuery(ctx, jwt, q)
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
				return nil
			}
			mu.Lock()
			locs[id] = LocationCoords{Lat: coords.Value.Latitude, Lon: coords.Value.Longitude}
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
