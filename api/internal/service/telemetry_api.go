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
type TelemetryAPIService interface {
	Latest(tenant models.Tenant, tokenID uint64, signals []string) (map[string]SignalLatest, error)
	LatestLocation(tenant models.Tenant, tokenID uint64) (*VehicleLocation, error)
	TimeSeries(tenant models.Tenant, tokenID uint64, signal, from, to, interval string) ([]TimeSeriesBucket, error)
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
func (t *telemetryAPIService) TimeSeries(tenant models.Tenant, tokenID uint64, signal, from, to, interval string) ([]TimeSeriesBucket, error) {
	q := fmt.Sprintf(`query {
		signals(tokenId: %d, from: %q, to: %q, interval: %q) {
			timestamp
			%s { min max avg last }
		}
	}`, tokenID, from, to, interval, signal)

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
		if v, ok := bucket[signal]; ok {
			var agg struct {
				Min  float64 `json:"min"`
				Max  float64 `json:"max"`
				Avg  float64 `json:"avg"`
				Last float64 `json:"last"`
			}
			_ = json.Unmarshal(v, &agg)
			b.Min, b.Max, b.Avg, b.Last = agg.Min, agg.Max, agg.Avg, agg.Last
		}
		buckets = append(buckets, b)
	}
	return buckets, nil
}

func (t *telemetryAPIService) query(tenant models.Tenant, tokenID uint64, gql string) ([]byte, error) {
	jwt, err := t.authProvider.GetVehicleJWT(tenant, tokenID)
	if err != nil {
		return nil, fmt.Errorf("vehicle JWT: %w", err)
	}
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

	// GraphQL surfaces partial errors in body — surface the first as an error
	// so callers can branch (e.g. "permission denied" vs "field not selected").
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
