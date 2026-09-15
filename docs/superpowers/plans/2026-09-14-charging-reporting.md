# EV Charging Reporting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Charging" tab that detects EV charging sessions from DIMO telemetry, maps where they happened, and reports kWh added, electricity cost, and $ saved vs. gasoline per session/vehicle/fleet.

**Architecture:** A new `ChargingDetectionService` mirrors `GeofenceDetectionService`'s scan-and-persist pattern (fetch only the telemetry gap not yet covered, detect, persist, read back) but with no per-geofence dimension — one coverage ledger per vehicle. A new `TenantChargingSettingsService` holds the tenant's rate/efficiency assumptions and computes cost/$-saved at read time so an edited rate re-prices history live. A new `ChargingController` exposes both over HTTP, and a new `charging-view.ts` (map + table + settings panel) sits as a top-level tab next to TCO.

**Tech Stack:** Go + Fiber + PostgreSQL + SQLBoiler (backend), Lit + TypeScript + Vite + Leaflet (frontend). Follows this repo's existing geofence-detection and TCO patterns exactly — no new external integrations.

**Spec:** `docs/superpowers/specs/2026-09-14-charging-reporting-design.md`

## Global Constraints

- Tenant-level rate settings only — no per-vehicle or per-session overrides (this round).
- Cost and $-saved-vs-gas are computed at read time from stored kWh, never persisted on the session row, so an edited rate retroactively re-prices history.
- No new external integrations — session detection uses only the existing DIMO Telemetry API `signals` query, same auth path as trips/geofences.
- Combustion/hybrid vehicles that never report `powertrainTractionBatteryChargingIsCharging` are excluded from totals, not shown as zero.
- A session with fewer than 2 samples is discarded (can't measure an energy delta).
- Follow existing repo conventions exactly: SQLBoiler-generated models (never hand-edit `internal/db/models/*.go`), `make migrate` + `make sqlboiler` for schema changes, per-controller `vehicleInTenant` helper matching `tco.go`, Lit + `@lit/localize` `msg()` for all user-facing strings.
- This repo has no DB-backed test harness (confirmed: `tco_service_test.go` and `geofence_detection_test.go` only unit-test pure functions). Follow that precedent — pure-function unit tests for detection/cost math, no new test infrastructure, manual Chrome verification for the rest.

---

### Task 1: Migration + SQLBoiler models for charging tables

**Files:**
- Create: `api/internal/db/migrations/20260914160000_charging_sessions.sql`
- Generated (do not hand-edit): `api/internal/db/models/charging_session.go`, `api/internal/db/models/charging_scan_coverage.go`, `api/internal/db/models/tenant_charging_setting.go`

**Interfaces:**
- Produces: SQLBoiler models `dbmodels.ChargingSession` (fields `TenantID string`, `TokenID int64`, `StartedAt time.Time`, `EndedAt time.Time`, `AddedEnergyKwh null.Float64`, `AvgPowerKw null.Float64`, `SocStartPct null.Float64`, `SocEndPct null.Float64`, `Lat null.Float64`, `Lng null.Float64`, `NumSamples int`, `CreatedAt time.Time`), `dbmodels.ChargingSessionColumns`, query builder `dbmodels.ChargingSessions(mods...)`; `dbmodels.ChargingScanCoverage` (`TenantID`, `TokenID`, `ScannedFrom time.Time`, `ScannedTo time.Time`, `CreatedAt time.Time`), `dbmodels.ChargingScanCoverages(mods...)`; `dbmodels.TenantChargingSetting` (`TenantID string`, `ElectricityRate types.NullDecimal`, `GasPrice types.NullDecimal`, `GasMpgEquivalent types.NullDecimal`, `VehicleKwhPerMile types.NullDecimal`, `Currency string`, `CreatedAt`, `UpdatedAt`), `dbmodels.TenantChargingSettingColumns`, `dbmodels.FindTenantChargingSetting(ctx, exec, tenantID string, selectCols ...string) (*TenantChargingSetting, error)`. Tasks 3–5 consume these.

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- charging_sessions is the cached, summary-form result of EV charging-session
-- detection: one row per contiguous interval a vehicle's traction battery
-- reported isCharging=true, computed on-demand from telemetry and cached
-- because the past is immutable. Energy/power/SOC/location fields are
-- nullable — an interval that didn't report a given signal just omits it
-- rather than reporting a false 0. See
-- docs/superpowers/specs/2026-09-14-charging-reporting-design.md.
CREATE TABLE IF NOT EXISTS charging_sessions (
    tenant_id        UUID NOT NULL,
    token_id         BIGINT NOT NULL,
    started_at       TIMESTAMPTZ NOT NULL,
    ended_at         TIMESTAMPTZ NOT NULL,
    added_energy_kwh DOUBLE PRECISION,
    avg_power_kw     DOUBLE PRECISION,
    soc_start_pct    DOUBLE PRECISION,
    soc_end_pct      DOUBLE PRECISION,
    lat              DOUBLE PRECISION,
    lng              DOUBLE PRECISION,
    num_samples      INTEGER NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id, started_at)
);

CREATE INDEX IF NOT EXISTS idx_charging_sessions_token ON charging_sessions (tenant_id, token_id, started_at);

-- charging_scan_coverage records which (vehicle, time-range) windows have
-- already been analyzed, independent of whether any session was found —
-- mirrors geofence_scan_coverage, minus the per-geofence dimension (charging
-- detection isn't scoped to a drawn shape). Before computing, the requested
-- window is checked against existing coverage so a repeat request for the
-- same range never re-fetches telemetry.
CREATE TABLE IF NOT EXISTS charging_scan_coverage (
    tenant_id    UUID   NOT NULL,
    token_id     BIGINT NOT NULL,
    scanned_from TIMESTAMPTZ NOT NULL,
    scanned_to   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id, scanned_from)
);

-- tenant_charging_settings holds one tenant's electricity-rate and gas-price
-- assumptions used to price charging sessions and estimate $ saved vs.
-- gasoline. Optional — no row means the Charging tab still shows kWh totals
-- but leaves cost/savings blank.
CREATE TABLE IF NOT EXISTS tenant_charging_settings (
    tenant_id            UUID PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    electricity_rate     NUMERIC,
    gas_price            NUMERIC,
    gas_mpg_equivalent   NUMERIC,
    vehicle_kwh_per_mile NUMERIC,
    currency             TEXT NOT NULL DEFAULT 'USD',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP TABLE IF EXISTS tenant_charging_settings;
DROP TABLE IF EXISTS charging_scan_coverage;
DROP TABLE IF EXISTS charging_sessions;

-- +goose StatementEnd
```

- [ ] **Step 2: Run the migration against your local dev DB**

Run: `cd api && make migrate`
Expected: output includes `OK   20260914160000_charging_sessions.sql` and no errors.

- [ ] **Step 3: Regenerate SQLBoiler models**

Run: `cd api && make sqlboiler`
Expected: `internal/db/models/charging_session.go`, `charging_scan_coverage.go`, `tenant_charging_setting.go` created (generated — do not hand-edit).

- [ ] **Step 4: Verify the build still compiles**

Run: `cd api && go build ./...`
Expected: no errors (nothing references the new models yet, so this only confirms the generated code itself compiles).

- [ ] **Step 5: Commit**

```bash
git add api/internal/db/migrations/20260914160000_charging_sessions.sql api/internal/db/models/charging_session.go api/internal/db/models/charging_scan_coverage.go api/internal/db/models/tenant_charging_setting.go
git commit -m "feat(api): add charging_sessions, charging_scan_coverage, tenant_charging_settings tables"
```

---

### Task 2: `TelemetryAPIService.ChargingSamples`

**Files:**
- Modify: `api/internal/service/telemetry_api.go`

**Interfaces:**
- Consumes: `t.query(tenant, tokenID, gql)` (existing private method), `rfc3339` (existing helper, `geofence_detection.go:690`).
- Produces: `ChargingSample` struct and `TelemetryAPIService.ChargingSamples(tenant models.Tenant, tokenID uint64, from, to, interval string) ([]ChargingSample, error)`. Task 4 consumes this via the `TelemetryAPIService` interface.

- [ ] **Step 1: Add the `ChargingSample` type and interface method**

Add to the `TelemetryAPIService` interface (after `GeofenceSamples`):

```go
	// ChargingSamples returns interval-bucketed EV charging-signal readings
	// over a window, ordered by time — the input to charging-session
	// detection. interval is a telemetry-api duration (e.g. "30s").
	ChargingSamples(tenant models.Tenant, tokenID uint64, from, to, interval string) ([]ChargingSample, error)
```

Add near `GeoSample`:

```go
// ChargingSample is one interval-bucketed telemetry reading used for
// charging-session detection. Location is included when reported so a
// detected session can be placed on the map; Cable-connected is deliberately
// not queried — nothing downstream consumes it, since a session is defined
// purely by IsCharging.
type ChargingSample struct {
	Time           time.Time
	IsCharging     *bool
	AddedEnergyKwh *float64
	PowerKw        *float64
	SocPct         *float64
	Lat            *float64
	Lng            *float64
}
```

- [ ] **Step 2: Implement `ChargingSamples`**

Add after `GeofenceSamples`:

```go
func (t *telemetryAPIService) ChargingSamples(tenant models.Tenant, tokenID uint64, from, to, interval string) ([]ChargingSample, error) {
	if interval == "" {
		interval = "30s"
	}
	q := fmt.Sprintf(`query {
		samples: signals(tokenId: %d, from: %q, to: %q, interval: %q) {
			timestamp
			powertrainTractionBatteryChargingIsCharging(agg: LAST)
			powertrainTractionBatteryChargingAddedEnergy(agg: LAST)
			powertrainTractionBatteryChargingPower(agg: LAST)
			powertrainTractionBatteryStateOfChargeCurrent(agg: LAST)
			currentLocationCoordinates(agg: LAST) { latitude longitude }
		}
	}`, tokenID, from, to, interval)

	raw, err := t.query(tenant, tokenID, q)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			Samples []struct {
				Timestamp                                     string   `json:"timestamp"`
				PowertrainTractionBatteryChargingIsCharging   *bool    `json:"powertrainTractionBatteryChargingIsCharging"`
				PowertrainTractionBatteryChargingAddedEnergy  *float64 `json:"powertrainTractionBatteryChargingAddedEnergy"`
				PowertrainTractionBatteryChargingPower        *float64 `json:"powertrainTractionBatteryChargingPower"`
				PowertrainTractionBatteryStateOfChargeCurrent *float64 `json:"powertrainTractionBatteryStateOfChargeCurrent"`
				CurrentLocationCoordinates                    *struct {
					Latitude  float64 `json:"latitude"`
					Longitude float64 `json:"longitude"`
				} `json:"currentLocationCoordinates"`
			} `json:"samples"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse charging samples: %w", err)
	}

	out := make([]ChargingSample, 0, len(resp.Data.Samples))
	for _, s := range resp.Data.Samples {
		ts, perr := time.Parse(time.RFC3339, s.Timestamp)
		if perr != nil {
			continue
		}
		cs := ChargingSample{
			Time:           ts,
			IsCharging:     s.PowertrainTractionBatteryChargingIsCharging,
			AddedEnergyKwh: s.PowertrainTractionBatteryChargingAddedEnergy,
			PowerKw:        s.PowertrainTractionBatteryChargingPower,
			SocPct:         s.PowertrainTractionBatteryStateOfChargeCurrent,
		}
		if s.CurrentLocationCoordinates != nil {
			lat, lng := s.CurrentLocationCoordinates.Latitude, s.CurrentLocationCoordinates.Longitude
			cs.Lat, cs.Lng = &lat, &lng
		}
		out = append(out, cs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })

	t.logger.Info().Uint64("tokenID", tokenID).Str("from", from).Str("to", to).
		Int("samples", len(out)).Msg("telemetry charging samples fetched")

	return out, nil
}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd api && go build ./...`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add api/internal/service/telemetry_api.go
git commit -m "feat(api): add ChargingSamples to TelemetryAPIService"
```

---

### Task 3: Charging-session detection sweep (TDD)

**Files:**
- Create: `api/internal/service/charging_detection.go`
- Test: `api/internal/service/charging_detection_test.go`

**Interfaces:**
- Consumes: `ChargingSample` (Task 2).
- Produces: `detectedChargingSession` (unexported struct with `startedAt`, `endedAt time.Time`, `lat`, `lng *float64`, `socStart`, `socEnd *float64`, `numSamples int`), its methods `addedEnergyKwh() *float64` and `avgPowerKw() *float64`, and `detectChargingSessions(samples []ChargingSample) []detectedChargingSession`. Task 4 consumes `detectChargingSessions` and the accessor methods.

- [ ] **Step 1: Write the failing tests**

```go
// api/internal/service/charging_detection_test.go
package service

import (
	"testing"
	"time"
)

func chargingSample(sec int, charging *bool, addedEnergy, power, soc, lat, lng *float64) ChargingSample {
	return ChargingSample{
		Time:           time.Date(2026, 9, 1, 8, 0, sec, 0, time.UTC),
		IsCharging:     charging,
		AddedEnergyKwh: addedEnergy,
		PowerKw:        power,
		SocPct:         soc,
		Lat:            lat,
		Lng:            lng,
	}
}

func bptr(b bool) *bool       { return &b }
func f64ptr(f float64) *float64 { return &f }

func TestDetectChargingSessions_SingleSession(t *testing.T) {
	lat, lng := 37.7749, -122.4194
	samples := []ChargingSample{
		chargingSample(0, bptr(false), nil, nil, f64ptr(40), nil, nil),
		chargingSample(30, bptr(true), f64ptr(10.0), f64ptr(7.1), f64ptr(41), &lat, &lng),
		chargingSample(60, bptr(true), f64ptr(15.5), f64ptr(7.0), f64ptr(48), &lat, &lng),
		chargingSample(90, bptr(true), f64ptr(20.0), f64ptr(6.8), f64ptr(55), &lat, &lng),
		chargingSample(120, bptr(false), nil, nil, f64ptr(55), nil, nil),
	}
	sessions := detectChargingSessions(samples)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	s := sessions[0]
	if s.numSamples != 3 {
		t.Fatalf("numSamples = %d, want 3", s.numSamples)
	}
	if got := s.addedEnergyKwh(); got == nil || *got != 10.0 {
		t.Fatalf("addedEnergyKwh = %v, want 10.0", got)
	}
	if got := s.avgPowerKw(); got == nil || *got != 6.966666666666667 {
		t.Fatalf("avgPowerKw = %v, want ~6.97", got)
	}
	if s.socStart == nil || *s.socStart != 41 || s.socEnd == nil || *s.socEnd != 55 {
		t.Fatalf("soc start/end = %v/%v, want 41/55", s.socStart, s.socEnd)
	}
	if s.lat == nil || *s.lat != lat {
		t.Fatalf("lat = %v, want %v", s.lat, lat)
	}
}

func TestDetectChargingSessions_TooFewSamplesDiscarded(t *testing.T) {
	samples := []ChargingSample{
		chargingSample(0, bptr(false), nil, nil, nil, nil, nil),
		chargingSample(30, bptr(true), f64ptr(5.0), f64ptr(7.0), nil, nil, nil),
		chargingSample(60, bptr(false), nil, nil, nil, nil, nil),
	}
	if got := detectChargingSessions(samples); len(got) != 0 {
		t.Fatalf("got %d sessions, want 0 (single-sample session discarded)", len(got))
	}
}

func TestDetectChargingSessions_CableConnectedNotChargingOpensNoSession(t *testing.T) {
	// IsCharging false throughout — a connected-but-not-charging vehicle
	// (e.g. finished charging, or on a schedule delay) must not open a
	// session even though it may be plugged in.
	samples := []ChargingSample{
		chargingSample(0, bptr(false), nil, nil, nil, nil, nil),
		chargingSample(30, bptr(false), nil, nil, nil, nil, nil),
		chargingSample(60, bptr(false), nil, nil, nil, nil, nil),
	}
	if got := detectChargingSessions(samples); len(got) != 0 {
		t.Fatalf("got %d sessions, want 0", len(got))
	}
}

func TestDetectChargingSessions_BriefDropoutSplitsSession(t *testing.T) {
	// isCharging flickers false for one sample mid-session. Documented
	// limitation, same as GeofenceDetectionService.detectPasses: a gap of
	// any length ends the current run. Two 2-sample sessions, not one.
	samples := []ChargingSample{
		chargingSample(0, bptr(true), f64ptr(1.0), f64ptr(7.0), nil, nil, nil),
		chargingSample(30, bptr(true), f64ptr(2.0), f64ptr(7.0), nil, nil, nil),
		chargingSample(60, bptr(false), nil, nil, nil, nil, nil),
		chargingSample(90, bptr(true), f64ptr(3.0), f64ptr(7.0), nil, nil, nil),
		chargingSample(120, bptr(true), f64ptr(4.0), f64ptr(7.0), nil, nil, nil),
	}
	sessions := detectChargingSessions(samples)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
}

func TestDetectChargingSessions_EnergyCounterResetYieldsNilEnergy(t *testing.T) {
	// AddedEnergy counter resets mid-session (e.g. a new charge cycle
	// re-zeroed it) — last-minus-first goes negative. Same guard as
	// GeofenceDetectionService.engineRuntimeS: report nil, never a negative.
	samples := []ChargingSample{
		chargingSample(0, bptr(true), f64ptr(18.0), nil, nil, nil, nil),
		chargingSample(30, bptr(true), f64ptr(2.0), nil, nil, nil, nil),
		chargingSample(60, bptr(true), f64ptr(4.0), nil, nil, nil, nil),
	}
	sessions := detectChargingSessions(samples)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if got := sessions[0].addedEnergyKwh(); got != nil {
		t.Fatalf("addedEnergyKwh = %v, want nil", got)
	}
}

func TestDetectChargingSessions_NilIsChargingTreatedAsNotCharging(t *testing.T) {
	samples := []ChargingSample{
		chargingSample(0, nil, nil, nil, nil, nil, nil),
		chargingSample(30, nil, nil, nil, nil, nil, nil),
	}
	if got := detectChargingSessions(samples); len(got) != 0 {
		t.Fatalf("got %d sessions, want 0", len(got))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/service/... -run TestDetectChargingSessions -v`
Expected: FAIL with "undefined: detectChargingSessions" (or similar — the function doesn't exist yet).

- [ ] **Step 3: Implement `charging_detection.go`**

```go
// api/internal/service/charging_detection.go
package service

import "time"

// detectedChargingSession is the internal result of the isCharging sweep
// before persistence.
type detectedChargingSession struct {
	startedAt      time.Time
	endedAt        time.Time
	lat            *float64
	lng            *float64
	socStart       *float64
	socEnd         *float64
	numSamples     int
	firstEnergyKwh *float64
	lastEnergyKwh  *float64
	powerSumKw     float64
	powerSamples   int
}

// addedEnergyKwh is the session's last-minus-first AddedEnergyKwh reading.
// Returns nil when fewer than one energy reading was seen, or when the delta
// is negative (the counter reset mid-session — e.g. a new charge cycle
// re-zeroed it) — a negative number would be meaningless, mirroring
// GeofenceDetectionService's engineRuntimeS guard.
func (s detectedChargingSession) addedEnergyKwh() *float64 {
	if s.firstEnergyKwh == nil || s.lastEnergyKwh == nil {
		return nil
	}
	d := *s.lastEnergyKwh - *s.firstEnergyKwh
	if d < 0 {
		return nil
	}
	return &d
}

// avgPowerKw averages every PowerKw reading seen during the session. Returns
// nil when the vehicle never reported it.
func (s detectedChargingSession) avgPowerKw() *float64 {
	if s.powerSamples == 0 {
		return nil
	}
	avg := s.powerSumKw / float64(s.powerSamples)
	return &avg
}

// detectChargingSessions sweeps ordered samples and emits one session per
// maximal run of consecutive IsCharging=true samples (nil treated as false —
// a vehicle with no charging-signal support simply never opens a session).
// A run shorter than 2 samples is discarded: a single point can't measure an
// energy delta. Mirrors GeofenceDetectionService.detectPasses; the same
// documented limitation applies — a brief signal dropout mid-session (one
// sample flickering false) splits it into two sessions rather than being
// bridged.
func detectChargingSessions(samples []ChargingSample) []detectedChargingSession {
	var sessions []detectedChargingSession
	var cur *detectedChargingSession
	flush := func() {
		if cur != nil && cur.numSamples >= 2 {
			sessions = append(sessions, *cur)
		}
		cur = nil
	}
	for _, smp := range samples {
		charging := smp.IsCharging != nil && *smp.IsCharging
		if !charging {
			flush()
			continue
		}
		if cur == nil {
			cur = &detectedChargingSession{startedAt: smp.Time}
		}
		cur.endedAt = smp.Time
		cur.numSamples++
		if smp.Lat != nil && smp.Lng != nil {
			lat, lng := *smp.Lat, *smp.Lng
			cur.lat, cur.lng = &lat, &lng
		}
		if smp.SocPct != nil {
			v := *smp.SocPct
			if cur.socStart == nil {
				cur.socStart = &v
			}
			cur.socEnd = &v
		}
		if smp.AddedEnergyKwh != nil {
			v := *smp.AddedEnergyKwh
			if cur.firstEnergyKwh == nil {
				cur.firstEnergyKwh = &v
			}
			cur.lastEnergyKwh = &v
		}
		if smp.PowerKw != nil {
			cur.powerSumKw += *smp.PowerKw
			cur.powerSamples++
		}
	}
	flush()
	return sessions
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go test ./internal/service/... -run TestDetectChargingSessions -v`
Expected: PASS (all 6 subtests).

- [ ] **Step 5: Commit**

```bash
git add api/internal/service/charging_detection.go api/internal/service/charging_detection_test.go
git commit -m "feat(api): add charging-session detection sweep"
```

---

### Task 4: `ChargingDetectionService` (scan, persist, read)

**Files:**
- Create: `api/internal/service/charging_detection_service.go`

**Interfaces:**
- Consumes: `TelemetryAPIService.ChargingSamples` (Task 2), `detectChargingSessions` (Task 3), `rfc3339` (existing, `geofence_detection.go:690`), `dbmodels.ChargingSession`/`ChargingScanCoverage` (Task 1), `models.Tenant` (existing).
- Produces: `ChargingDetectionService` with constructor `NewChargingDetectionService(logger *zerolog.Logger, pdb *db.Store, telemetry TelemetryAPIService) *ChargingDetectionService` and method `Sessions(ctx context.Context, tenant models.Tenant, tokenID int64, from, to time.Time) ([]dbmodels.ChargingSession, error)`. Task 6 (controller) and Task 5 (cost computation) consume `Sessions`' returned rows.

- [ ] **Step 1: Implement the service**

```go
// api/internal/service/charging_detection_service.go
package service

import (
	"context"
	"fmt"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/rs/zerolog"
)

// chargingSampleInterval matches geofenceSampleInterval — same telemetry
// bucketing cadence used for trip-replay and geofence detection.
const chargingSampleInterval = "30s"

// ChargingDetectionService computes EV charging sessions from telemetry and
// caches them, on demand. Past telemetry is immutable, so a computed session
// never goes stale; a scan-coverage ledger prevents recomputation. Mirrors
// GeofenceDetectionService with no per-geofence dimension — charging isn't
// scoped to a drawn shape, so coverage is tracked per vehicle only.
type ChargingDetectionService struct {
	logger    *zerolog.Logger
	pdb       *db.Store
	telemetry TelemetryAPIService
}

func NewChargingDetectionService(logger *zerolog.Logger, pdb *db.Store, telemetry TelemetryAPIService) *ChargingDetectionService {
	return &ChargingDetectionService{logger: logger, pdb: pdb, telemetry: telemetry}
}

// Sessions returns a vehicle's charging sessions overlapping [from, to],
// computing only the gap not already covered by charging_scan_coverage.
func (s *ChargingDetectionService) Sessions(ctx context.Context, tenant models.Tenant, tokenID int64, from, to time.Time) ([]dbmodels.ChargingSession, error) {
	covered, err := s.isCovered(ctx, tokenID, from, to)
	if err != nil {
		return nil, err
	}
	if !covered {
		samples, serr := s.telemetry.ChargingSamples(tenant, uint64(tokenID), rfc3339(from), rfc3339(to), chargingSampleInterval)
		if serr != nil {
			return nil, fmt.Errorf("charging samples: %w", serr)
		}
		if perr := s.persistSessions(ctx, tenant.ID, tokenID, samples, from, to); perr != nil {
			return nil, perr
		}
		if cerr := s.recordCoverage(ctx, tenant.ID, tokenID, from, to); cerr != nil {
			return nil, cerr
		}
	}
	return s.readSessions(ctx, tokenID, from, to)
}

// isCovered reports whether an existing charging_scan_coverage row for this
// vehicle already fully contains [from, to].
func (s *ChargingDetectionService) isCovered(ctx context.Context, tokenID int64, from, to time.Time) (bool, error) {
	rows, err := dbmodels.ChargingScanCoverages(
		dbmodels.ChargingScanCoverageWhere.TokenID.EQ(tokenID),
	).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return false, fmt.Errorf("load charging scan coverage: %w", err)
	}
	for _, r := range rows {
		if !r.ScannedFrom.After(from) && !r.ScannedTo.Before(to) {
			return true, nil
		}
	}
	return false, nil
}

// persistSessions runs the detection sweep over samples and replaces any
// previously-detected sessions in [from, to] with the fresh set — a re-scan
// of an overlapping window can yield sessions with slightly shifted
// started_at (telemetry-api buckets are window-relative), so deleting the
// window first keeps exactly one copy and makes recompute idempotent.
// Mirrors GeofenceDetectionService.persistPasses.
func (s *ChargingDetectionService) persistSessions(ctx context.Context, tenantID string, tokenID int64, samples []ChargingSample, from, to time.Time) error {
	detected := detectChargingSessions(samples)

	writer := s.pdb.DBS().Writer
	if _, err := dbmodels.ChargingSessions(
		dbmodels.ChargingSessionWhere.TokenID.EQ(tokenID),
		qm.Where("started_at >= ? AND started_at <= ?", from, to),
	).DeleteAll(ctx, writer); err != nil {
		return fmt.Errorf("clear stale charging sessions: %w", err)
	}
	for _, d := range detected {
		m := &dbmodels.ChargingSession{
			TenantID:       tenantID,
			TokenID:        tokenID,
			StartedAt:      d.startedAt,
			EndedAt:        d.endedAt,
			AddedEnergyKwh: null.Float64FromPtr(d.addedEnergyKwh()),
			AvgPowerKw:     null.Float64FromPtr(d.avgPowerKw()),
			SocStartPct:    null.Float64FromPtr(d.socStart),
			SocEndPct:      null.Float64FromPtr(d.socEnd),
			Lat:            null.Float64FromPtr(d.lat),
			Lng:            null.Float64FromPtr(d.lng),
			NumSamples:     d.numSamples,
		}
		if err := m.Insert(ctx, writer, boil.Infer()); err != nil {
			return fmt.Errorf("insert charging session: %w", err)
		}
	}
	return nil
}

// recordCoverage marks [from, to] as scanned for this vehicle.
func (s *ChargingDetectionService) recordCoverage(ctx context.Context, tenantID string, tokenID int64, from, to time.Time) error {
	cov := &dbmodels.ChargingScanCoverage{
		TenantID:    tenantID,
		TokenID:     tokenID,
		ScannedFrom: from,
		ScannedTo:   to,
	}
	if err := cov.Upsert(ctx, s.pdb.DBS().Writer, true, []string{"tenant_id", "token_id", "scanned_from"}, boil.Whitelist("scanned_to"), boil.Infer()); err != nil {
		return fmt.Errorf("upsert charging coverage: %w", err)
	}
	return nil
}

// readSessions reads persisted sessions for one vehicle overlapping [from, to].
func (s *ChargingDetectionService) readSessions(ctx context.Context, tokenID int64, from, to time.Time) ([]dbmodels.ChargingSession, error) {
	rows, err := dbmodels.ChargingSessions(
		dbmodels.ChargingSessionWhere.TokenID.EQ(tokenID),
		qm.Where("started_at >= ? AND started_at <= ?", from, to),
		qm.OrderBy(dbmodels.ChargingSessionColumns.StartedAt),
	).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, fmt.Errorf("read charging sessions: %w", err)
	}
	out := make([]dbmodels.ChargingSession, len(rows))
	for i, r := range rows {
		out[i] = *r
	}
	return out, nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd api && go build ./...`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add api/internal/service/charging_detection_service.go
git commit -m "feat(api): add ChargingDetectionService scan/persist/read"
```

---

### Task 5: Tenant charging settings + cost/savings computation (TDD)

**Files:**
- Create: `api/internal/service/charging_settings_service.go`
- Test: `api/internal/service/charging_settings_service_test.go`

**Interfaces:**
- Consumes: `dbmodels.TenantChargingSetting` (Task 1), `dbmodels.ChargingSession` (Task 1).
- Produces: `ChargingSettings` struct (`ElectricityRate`, `GasPrice`, `GasMpgEquivalent`, `VehicleKwhPerMile *float64`, `Currency string`), `ChargingSessionCost` struct (`Cost`, `GasCostAvoided`, `Savings *float64`), `computeSessionCost(addedEnergyKwh *float64, settings ChargingSettings) ChargingSessionCost`, and `ChargingSettingsService` with `NewChargingSettingsService(pdb *db.Store) *ChargingSettingsService`, `GetSettings(ctx, tenantID string) (ChargingSettings, error)`, `UpsertSettings(ctx, tenantID string, in ChargingSettings) error`. Task 6 (controller) and Task 7 (CSV) consume all of these.

- [ ] **Step 1: Write the failing tests**

```go
// api/internal/service/charging_settings_service_test.go
package service

import "testing"

func TestComputeSessionCost_FullSettings(t *testing.T) {
	energy := 30.0
	settings := ChargingSettings{
		ElectricityRate:   f64ptr(0.15),
		GasPrice:          f64ptr(3.50),
		GasMpgEquivalent:  f64ptr(30),
		VehicleKwhPerMile: f64ptr(0.30),
	}
	got := computeSessionCost(&energy, settings)
	if got.Cost == nil || *got.Cost != 4.5 {
		t.Fatalf("cost = %v, want 4.5", got.Cost)
	}
	// miles enabled = 30/0.30 = 100; gallons avoided = 100/30 = 3.3333...;
	// gas cost avoided = 3.3333... * 3.50 = 11.6666...
	if got.GasCostAvoided == nil || round2(*got.GasCostAvoided) != 11.67 {
		t.Fatalf("gasCostAvoided = %v, want ~11.67", got.GasCostAvoided)
	}
	if got.Savings == nil || round2(*got.Savings) != 7.17 {
		t.Fatalf("savings = %v, want ~7.17", got.Savings)
	}
}

func TestComputeSessionCost_NoSettings(t *testing.T) {
	energy := 30.0
	got := computeSessionCost(&energy, ChargingSettings{})
	if got.Cost != nil || got.GasCostAvoided != nil || got.Savings != nil {
		t.Fatalf("got %+v, want all nil (no settings configured)", got)
	}
}

func TestComputeSessionCost_NilEnergy(t *testing.T) {
	settings := ChargingSettings{
		ElectricityRate:   f64ptr(0.15),
		GasPrice:          f64ptr(3.50),
		GasMpgEquivalent:  f64ptr(30),
		VehicleKwhPerMile: f64ptr(0.30),
	}
	got := computeSessionCost(nil, settings)
	if got.Cost != nil || got.GasCostAvoided != nil || got.Savings != nil {
		t.Fatalf("got %+v, want all nil (no energy reading)", got)
	}
}

func TestComputeSessionCost_ZeroEfficiencyDoesNotDivideByZero(t *testing.T) {
	energy := 30.0
	settings := ChargingSettings{
		ElectricityRate:   f64ptr(0.15),
		GasPrice:          f64ptr(3.50),
		GasMpgEquivalent:  f64ptr(30),
		VehicleKwhPerMile: f64ptr(0), // misconfigured tenant setting
	}
	got := computeSessionCost(&energy, settings)
	if got.Cost == nil || *got.Cost != 4.5 {
		t.Fatalf("cost = %v, want 4.5 (electricity cost still computable)", got.Cost)
	}
	if got.GasCostAvoided != nil || got.Savings != nil {
		t.Fatalf("got gasCostAvoided=%v savings=%v, want nil (efficiency is 0)", got.GasCostAvoided, got.Savings)
	}
}

func TestComputeSessionCost_ZeroMpgDoesNotDivideByZero(t *testing.T) {
	energy := 30.0
	settings := ChargingSettings{
		ElectricityRate:   f64ptr(0.15),
		GasPrice:          f64ptr(3.50),
		GasMpgEquivalent:  f64ptr(0), // misconfigured tenant setting
		VehicleKwhPerMile: f64ptr(0.30),
	}
	got := computeSessionCost(&energy, settings)
	if got.GasCostAvoided != nil || got.Savings != nil {
		t.Fatalf("got gasCostAvoided=%v savings=%v, want nil (mpg is 0)", got.GasCostAvoided, got.Savings)
	}
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/service/... -run TestComputeSessionCost -v`
Expected: FAIL with "undefined: computeSessionCost" (or similar).

- [ ] **Step 3: Implement `charging_settings_service.go`**

```go
// api/internal/service/charging_settings_service.go
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/types"
	"github.com/ericlagergren/decimal"
)

// ChargingSettings is a tenant's optional electricity-rate/gas-comparison
// assumptions. Nil pointer fields mean "not set" — cost/savings are left
// blank rather than computed from a guessed default.
type ChargingSettings struct {
	ElectricityRate   *float64 `json:"electricityRate,omitempty"`
	GasPrice          *float64 `json:"gasPrice,omitempty"`
	GasMpgEquivalent  *float64 `json:"gasMpgEquivalent,omitempty"`
	VehicleKwhPerMile *float64 `json:"vehicleKwhPerMile,omitempty"`
	Currency          string   `json:"currency"`
}

// ChargingSessionCost is one session's priced-out figures, computed at read
// time against the tenant's current settings. Any field is nil when the
// inputs it needs aren't available (missing setting, zero denominator, or no
// energy reading) — never a misleading zero.
type ChargingSessionCost struct {
	Cost           *float64 `json:"cost,omitempty"`
	GasCostAvoided *float64 `json:"gasCostAvoided,omitempty"`
	Savings        *float64 `json:"savings,omitempty"`
}

// computeSessionCost prices one session's added energy against the tenant's
// settings. electricity cost only needs ElectricityRate; the gas-comparison
// figures additionally need GasPrice, GasMpgEquivalent, and a nonzero
// VehicleKwhPerMile (it's a divisor — a misconfigured 0 must not panic or
// silently misreport, so the comparison is simply left blank).
func computeSessionCost(addedEnergyKwh *float64, settings ChargingSettings) ChargingSessionCost {
	var out ChargingSessionCost
	if addedEnergyKwh == nil {
		return out
	}
	if settings.ElectricityRate != nil {
		cost := *addedEnergyKwh * *settings.ElectricityRate
		out.Cost = &cost
	}
	if settings.GasPrice == nil || settings.GasMpgEquivalent == nil || *settings.GasMpgEquivalent == 0 ||
		settings.VehicleKwhPerMile == nil || *settings.VehicleKwhPerMile == 0 {
		return out
	}
	milesEnabled := *addedEnergyKwh / *settings.VehicleKwhPerMile
	gasCostAvoided := (milesEnabled / *settings.GasMpgEquivalent) * *settings.GasPrice
	out.GasCostAvoided = &gasCostAvoided
	if out.Cost != nil {
		savings := gasCostAvoided - *out.Cost
		out.Savings = &savings
	}
	return out
}

// ChargingSettingsService is simple CRUD over tenant_charging_settings.
type ChargingSettingsService struct {
	pdb *db.Store
}

func NewChargingSettingsService(pdb *db.Store) *ChargingSettingsService {
	return &ChargingSettingsService{pdb: pdb}
}

// GetSettings returns a tenant's charging settings, or a zero-value
// ChargingSettings (Currency defaulted to USD, other fields nil) if none
// have been saved.
func (s *ChargingSettingsService) GetSettings(ctx context.Context, tenantID string) (ChargingSettings, error) {
	row, err := dbmodels.FindTenantChargingSetting(ctx, s.pdb.DBS().Reader, tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ChargingSettings{Currency: "USD"}, nil
		}
		return ChargingSettings{}, fmt.Errorf("get charging settings: %w", err)
	}
	out := ChargingSettings{Currency: row.Currency}
	if f, ok := decimalFloat(row.ElectricityRate); ok {
		out.ElectricityRate = &f
	}
	if f, ok := decimalFloat(row.GasPrice); ok {
		out.GasPrice = &f
	}
	if f, ok := decimalFloat(row.GasMpgEquivalent); ok {
		out.GasMpgEquivalent = &f
	}
	if f, ok := decimalFloat(row.VehicleKwhPerMile); ok {
		out.VehicleKwhPerMile = &f
	}
	return out, nil
}

func decimalFloat(d types.NullDecimal) (float64, bool) {
	if d.Big == nil {
		return 0, false
	}
	return d.Float64()
}

// UpsertSettings full-replaces a tenant's charging settings. An empty
// in.Currency defaults to "USD".
func (s *ChargingSettingsService) UpsertSettings(ctx context.Context, tenantID string, in ChargingSettings) error {
	currency := in.Currency
	if currency == "" {
		currency = "USD"
	}
	row := &dbmodels.TenantChargingSetting{
		TenantID: tenantID,
		Currency: currency,
	}
	if in.ElectricityRate != nil {
		row.ElectricityRate = types.NewNullDecimal(new(decimal.Big).SetFloat64(*in.ElectricityRate))
	}
	if in.GasPrice != nil {
		row.GasPrice = types.NewNullDecimal(new(decimal.Big).SetFloat64(*in.GasPrice))
	}
	if in.GasMpgEquivalent != nil {
		row.GasMpgEquivalent = types.NewNullDecimal(new(decimal.Big).SetFloat64(*in.GasMpgEquivalent))
	}
	if in.VehicleKwhPerMile != nil {
		row.VehicleKwhPerMile = types.NewNullDecimal(new(decimal.Big).SetFloat64(*in.VehicleKwhPerMile))
	}
	now := time.Now()
	row.CreatedAt = now
	row.UpdatedAt = now
	return row.Upsert(ctx, s.pdb.DBS().Writer, true,
		[]string{dbmodels.TenantChargingSettingColumns.TenantID},
		boil.Whitelist(
			dbmodels.TenantChargingSettingColumns.ElectricityRate,
			dbmodels.TenantChargingSettingColumns.GasPrice,
			dbmodels.TenantChargingSettingColumns.GasMpgEquivalent,
			dbmodels.TenantChargingSettingColumns.VehicleKwhPerMile,
			dbmodels.TenantChargingSettingColumns.Currency,
			dbmodels.TenantChargingSettingColumns.UpdatedAt,
		),
		boil.Infer(),
	)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go test ./internal/service/... -run TestComputeSessionCost -v`
Expected: PASS (all 5 subtests).

- [ ] **Step 5: Commit**

```bash
git add api/internal/service/charging_settings_service.go api/internal/service/charging_settings_service_test.go
git commit -m "feat(api): add tenant charging settings CRUD + cost/savings computation"
```

---

### Task 6: Charging aggregation (fleet summary + CSV) and controller

**Files:**
- Create: `api/internal/service/charging_service.go`
- Create: `api/internal/controllers/charging.go`
- Modify: `api/internal/app/app.go`

**Interfaces:**
- Consumes: `ChargingDetectionService.Sessions` (Task 4), `ChargingSettingsService.GetSettings`/`UpsertSettings` (Task 5), `computeSessionCost` (Task 5), `vehicleLabel` (existing, `tco_service.go`), `VehicleService.ListVehicles`/`GetVehicle` (existing), `GetTenant`/`GetAllowedGroups`/`ScopeUnavailable` (existing, `controllers/common.go`).
- Produces: `ChargingSessionView` struct (JSON shape below), `ChargingService` with `NewChargingService(logger, pdb, detectionSvc, settingsSvc, vehicleSvc) *ChargingService`, `FleetSummary(ctx, tenant, allowedGroupIDs, from, to) (*ChargingFleetSummary, error)`, `VehicleSessions(ctx, tenant, tokenID, allowedGroupIDs, from, to) ([]ChargingSessionView, error)`, `BuildCSV(sessions []ChargingSessionView) string`; `ChargingController` with routes wired in `app.go`.

- [ ] **Step 1: Implement `charging_service.go`**

```go
// api/internal/service/charging_service.go
package service

import (
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/rs/zerolog"
)

// ChargingSessionView is one session, priced against current tenant
// settings, shaped for the API and CSV export.
type ChargingSessionView struct {
	VehicleTokenID int64      `json:"tokenId"`
	VehicleLabel   string     `json:"vehicleLabel"`
	VIN            string     `json:"vin,omitempty"`
	StartedAt      time.Time  `json:"startedAt"`
	EndedAt        time.Time  `json:"endedAt"`
	AddedEnergyKwh *float64   `json:"addedEnergyKwh,omitempty"`
	AvgPowerKw     *float64   `json:"avgPowerKw,omitempty"`
	SocStartPct    *float64   `json:"socStartPct,omitempty"`
	SocEndPct      *float64   `json:"socEndPct,omitempty"`
	Lat            *float64   `json:"lat,omitempty"`
	Lng            *float64   `json:"lng,omitempty"`
	Currency       string     `json:"currency"`
	ChargingSessionCost
}

// ChargingFleetTotals sums each session's figures across the fleet. Cost
// fields are nil (not zero) when no tenant settings are configured, so the
// UI can distinguish "$0 saved" from "not configured".
type ChargingFleetTotals struct {
	AddedEnergyKwh float64  `json:"addedEnergyKwh"`
	Cost           *float64 `json:"cost,omitempty"`
	Savings        *float64 `json:"savings,omitempty"`
}

// ChargingFleetSummary is the fleet-wide rollup: one point per session (for
// the map) plus fleet totals.
type ChargingFleetSummary struct {
	Sessions []ChargingSessionView `json:"sessions"`
	Fleet    ChargingFleetTotals   `json:"fleet"`
}

// ChargingService aggregates detected sessions (ChargingDetectionService)
// with tenant pricing settings (ChargingSettingsService) into the fleet
// summary and per-vehicle views the controller exposes.
type ChargingService struct {
	logger      *zerolog.Logger
	detectionSvc *ChargingDetectionService
	settingsSvc  *ChargingSettingsService
	vehicleSvc   *VehicleService
}

func NewChargingService(logger *zerolog.Logger, detectionSvc *ChargingDetectionService, settingsSvc *ChargingSettingsService, vehicleSvc *VehicleService) *ChargingService {
	return &ChargingService{logger: logger, detectionSvc: detectionSvc, settingsSvc: settingsSvc, vehicleSvc: vehicleSvc}
}

func toView(row dbmodels.ChargingSession, label, vin string, settings ChargingSettings) ChargingSessionView {
	energy := row.AddedEnergyKwh.Ptr()
	v := ChargingSessionView{
		VehicleTokenID:      row.TokenID,
		VehicleLabel:        label,
		VIN:                 vin,
		StartedAt:           row.StartedAt,
		EndedAt:             row.EndedAt,
		AddedEnergyKwh:      energy,
		AvgPowerKw:          row.AvgPowerKw.Ptr(),
		SocStartPct:         row.SocStartPct.Ptr(),
		SocEndPct:           row.SocEndPct.Ptr(),
		Lat:                 row.Lat.Ptr(),
		Lng:                 row.Lng.Ptr(),
		Currency:            settings.Currency,
		ChargingSessionCost: computeSessionCost(energy, settings),
	}
	return v
}

// VehicleSessions returns one vehicle's priced charging sessions in
// [from, to]. allowedGroupIDs scopes the lookup to a limited member's
// accessible groups (nil for owners/full-access members).
func (s *ChargingService) VehicleSessions(ctx context.Context, tenant models.Tenant, tokenID int64, allowedGroupIDs []string, from, to time.Time) ([]ChargingSessionView, error) {
	vehicle, err := s.vehicleSvc.GetVehicle(ctx, tenant, tokenID, allowedGroupIDs)
	if err != nil {
		return nil, fmt.Errorf("get vehicle: %w", err)
	}
	label := vehicleLabel(*vehicle)

	rows, err := s.detectionSvc.Sessions(ctx, tenant, tokenID, from, to)
	if err != nil {
		return nil, fmt.Errorf("charging sessions: %w", err)
	}
	settings, err := s.settingsSvc.GetSettings(ctx, tenant.ID)
	if err != nil {
		return nil, fmt.Errorf("get charging settings: %w", err)
	}
	out := make([]ChargingSessionView, len(rows))
	for i, r := range rows {
		out[i] = toView(r, label, vehicle.VIN, settings)
	}
	return out, nil
}

// FleetSummary builds the charging rollup for every vehicle in the tenant
// that reports charging telemetry. A vehicle with no charging signals (an
// ICE vehicle, or an unsupported connection) simply contributes no sessions
// — never an error for the fleet as a whole. A single vehicle's detection
// failure is logged and skipped, same isolation FleetLocations/TCO use.
func (s *ChargingService) FleetSummary(ctx context.Context, tenant models.Tenant, allowedGroupIDs []string, from, to time.Time) (*ChargingFleetSummary, error) {
	vehicles, err := s.vehicleSvc.ListVehicles(ctx, tenant, allowedGroupIDs)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	settings, err := s.settingsSvc.GetSettings(ctx, tenant.ID)
	if err != nil {
		return nil, fmt.Errorf("get charging settings: %w", err)
	}
	out := &ChargingFleetSummary{Sessions: []ChargingSessionView{}}
	for _, v := range vehicles {
		rows, serr := s.detectionSvc.Sessions(ctx, tenant, v.TokenID, from, to)
		if serr != nil {
			s.logger.Warn().Err(serr).Int64("tokenID", v.TokenID).Msg("charging sessions failed, skipping")
			continue
		}
		label := vehicleLabel(v)
		for _, r := range rows {
			view := toView(r, label, v.VIN, settings)
			out.Sessions = append(out.Sessions, view)
			if view.AddedEnergyKwh != nil {
				out.Fleet.AddedEnergyKwh += *view.AddedEnergyKwh
			}
			if view.Cost != nil {
				if out.Fleet.Cost == nil {
					out.Fleet.Cost = new(float64)
				}
				*out.Fleet.Cost += *view.Cost
			}
			if view.Savings != nil {
				if out.Fleet.Savings == nil {
					out.Fleet.Savings = new(float64)
				}
				*out.Fleet.Savings += *view.Savings
			}
		}
	}
	return out, nil
}

// BuildCSV renders sessions as CSV text, one row per session. Cost fields
// render as empty cells (not "0") when unpriced, so a spreadsheet can't
// misread "not configured" as "no savings".
func BuildChargingCSV(sessions []ChargingSessionView) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"vehicle", "vin", "startedAt", "endedAt", "addedEnergyKwh", "avgPowerKw", "cost", "gasCostAvoided", "savings", "currency"})
	for _, s := range sessions {
		_ = w.Write([]string{
			s.VehicleLabel,
			s.VIN,
			s.StartedAt.Format(time.RFC3339),
			s.EndedAt.Format(time.RFC3339),
			floatOrBlank(s.AddedEnergyKwh),
			floatOrBlank(s.AvgPowerKw),
			floatOrBlank(s.Cost),
			floatOrBlank(s.GasCostAvoided),
			floatOrBlank(s.Savings),
			s.Currency,
		})
	}
	w.Flush()
	return b.String()
}

func floatOrBlank(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', 2, 64)
}
```

- [ ] **Step 2: Implement the controller**

```go
// api/internal/controllers/charging.go
package controllers

import (
	"fmt"
	"strconv"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

type ChargingController struct {
	logger      *zerolog.Logger
	chargingSvc *service.ChargingService
	settingsSvc *service.ChargingSettingsService
	vehicleSvc  *service.VehicleService
}

func NewChargingController(logger *zerolog.Logger, chargingSvc *service.ChargingService, settingsSvc *service.ChargingSettingsService, vehicleSvc *service.VehicleService) *ChargingController {
	return &ChargingController{logger: logger, chargingSvc: chargingSvc, settingsSvc: settingsSvc, vehicleSvc: vehicleSvc}
}

// vehicleInTenant mirrors TCOController.vehicleInTenant exactly.
func (t *ChargingController) vehicleInTenant(c *fiber.Ctx, tenant models.Tenant, tokenID int64) error {
	allowed, _ := GetAllowedGroups(c)
	if _, err := t.vehicleSvc.GetVehicle(c.Context(), tenant, tokenID, allowed); err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}
	return nil
}

// parseWindow reads `from`/`to` query params (RFC3339), defaulting to the
// trailing 30 days when absent — charging tabs open to "recent activity",
// not an unbounded all-time scan.
func parseWindow(c *fiber.Ctx) (from, to time.Time, err error) {
	to = time.Now()
	from = to.AddDate(0, 0, -30)
	if q := c.Query("to"); q != "" {
		if to, err = time.Parse(time.RFC3339, q); err != nil {
			return from, to, fmt.Errorf("invalid to: %w", err)
		}
	}
	if q := c.Query("from"); q != "" {
		if from, err = time.Parse(time.RFC3339, q); err != nil {
			return from, to, fmt.Errorf("invalid from: %w", err)
		}
	}
	return from, to, nil
}

// GetSettings — GET /charging/settings.
func (t *ChargingController) GetSettings(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	settings, err := t.settingsSvc.GetSettings(c.Context(), tenant.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "get charging settings: "+err.Error())
	}
	return c.JSON(settings)
}

// PutSettings — PUT /charging/settings.
func (t *ChargingController) PutSettings(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	var req service.ChargingSettings
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body: "+err.Error())
	}
	if err := t.settingsSvc.UpsertSettings(c.Context(), tenant.ID, req); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "save charging settings: "+err.Error())
	}
	return c.JSON(req)
}

// GetSummary — GET /charging/summary?from&to. Fleet-wide rollup.
func (t *ChargingController) GetSummary(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	from, to, err := parseWindow(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	allowed, _ := GetAllowedGroups(c)
	summary, err := t.chargingSvc.FleetSummary(c.Context(), tenant, allowed, from, to)
	if err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		return fiber.NewError(fiber.StatusInternalServerError, "charging summary: "+err.Error())
	}
	return c.JSON(summary)
}

// GetVehicleSessions — GET /charging/:tokenId/sessions?from&to.
func (t *ChargingController) GetVehicleSessions(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := strconv.ParseInt(c.Params("tokenId"), 10, 64)
	if err != nil || tokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "valid tokenId path param required")
	}
	if err := t.vehicleInTenant(c, tenant, tokenID); err != nil {
		return err
	}
	from, to, err := parseWindow(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	allowed, _ := GetAllowedGroups(c)
	sessions, err := t.chargingSvc.VehicleSessions(c.Context(), tenant, tokenID, allowed, from, to)
	if err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		return fiber.NewError(fiber.StatusInternalServerError, "charging sessions: "+err.Error())
	}
	return c.JSON(fiber.Map{"sessions": sessions})
}

// ExportCSV — GET /charging/export.csv?from&to (optional ?tokenId=N).
func (t *ChargingController) ExportCSV(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	from, to, err := parseWindow(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	filename := "charging-export.csv"
	var sessions []service.ChargingSessionView
	if q := c.Query("tokenId"); q != "" {
		tokenID, perr := strconv.ParseInt(q, 10, 64)
		if perr != nil || tokenID == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "invalid tokenId")
		}
		if err := t.vehicleInTenant(c, tenant, tokenID); err != nil {
			return err
		}
		allowed, _ := GetAllowedGroups(c)
		sessions, err = t.chargingSvc.VehicleSessions(c.Context(), tenant, tokenID, allowed, from, to)
		if err != nil {
			if serr := ScopeUnavailable(err); serr != nil {
				return serr
			}
			return fiber.NewError(fiber.StatusInternalServerError, "charging sessions: "+err.Error())
		}
		filename = fmt.Sprintf("charging-vehicle-%d.csv", tokenID)
	} else {
		allowed, _ := GetAllowedGroups(c)
		fleet, ferr := t.chargingSvc.FleetSummary(c.Context(), tenant, allowed, from, to)
		if ferr != nil {
			if serr := ScopeUnavailable(ferr); serr != nil {
				return serr
			}
			return fiber.NewError(fiber.StatusInternalServerError, "charging summary: "+ferr.Error())
		}
		sessions = fleet.Sessions
	}
	csvText := service.BuildChargingCSV(sessions)
	c.Set(fiber.HeaderContentType, "text/csv")
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
	return c.SendString(csvText)
}
```

- [ ] **Step 3: Wire the service and routes into `app.go`**

Add directly after the existing TCO block (`tenantApp.Put("/tco/vehicle/:tokenId/backfill/:documentId", tcoCtrl.BackfillAmount)`), before `return app`:

```go
	// EV charging reporting (session detection from telemetry + tenant
	// electricity-rate/gas-comparison settings).
	chargingDetectionSvc := service.NewChargingDetectionService(logger, pdb, telemetryAPI)
	chargingSettingsSvc := service.NewChargingSettingsService(pdb)
	chargingSvc := service.NewChargingService(logger, chargingDetectionSvc, chargingSettingsSvc, vehicleSvc)
	chargingCtrl := controllers.NewChargingController(logger, chargingSvc, chargingSettingsSvc, vehicleSvc)
	tenantApp.Get("/charging/settings", chargingCtrl.GetSettings)
	tenantApp.Put("/charging/settings", chargingCtrl.PutSettings)
	tenantApp.Get("/charging/summary", chargingCtrl.GetSummary)
	tenantApp.Get("/charging/:tokenId/sessions", chargingCtrl.GetVehicleSessions)
	tenantApp.Get("/charging/export.csv", chargingCtrl.ExportCSV)
```

- [ ] **Step 4: Verify the full backend build and test suite pass**

Run: `cd api && go build ./... && go vet ./... && make test`
Expected: no errors, all tests pass (including Tasks 3 and 5's new tests).

- [ ] **Step 5: Commit**

```bash
git add api/internal/service/charging_service.go api/internal/controllers/charging.go api/internal/app/app.go
git commit -m "feat(api): add charging fleet summary, CSV export, and controller routes"
```

---

### Task 7: Frontend types

**Files:**
- Create: `web/src/types/charging.ts`

**Interfaces:**
- Produces: `ChargingSettings`, `ChargingSessionView`, `ChargingFleetTotals`, `ChargingFleetSummary` interfaces matching the backend JSON shapes from Task 6. Tasks 8–10 consume these.

- [ ] **Step 1: Write the types**

```typescript
// web/src/types/charging.ts

export interface ChargingSettings {
    electricityRate?: number;
    gasPrice?: number;
    gasMpgEquivalent?: number;
    vehicleKwhPerMile?: number;
    currency: string;
}

export interface ChargingSessionView {
    tokenId: number;
    vehicleLabel: string;
    vin?: string;
    startedAt: string;
    endedAt: string;
    addedEnergyKwh?: number;
    avgPowerKw?: number;
    socStartPct?: number;
    socEndPct?: number;
    lat?: number;
    lng?: number;
    currency: string;
    /** Undefined (not 0) when the tenant hasn't configured an electricity rate. */
    cost?: number;
    /** Undefined when gas-comparison settings (price/mpg/efficiency) are incomplete. */
    gasCostAvoided?: number;
    savings?: number;
}

export interface ChargingFleetTotals {
    addedEnergyKwh: number;
    cost?: number;
    savings?: number;
}

export interface ChargingFleetSummary {
    sessions: ChargingSessionView[];
    fleet: ChargingFleetTotals;
}
```

- [ ] **Step 2: Verify the type-checker passes**

Run: `cd web && npx tsc --noEmit`
Expected: no errors (this file has no consumers yet, so this only confirms it parses).

- [ ] **Step 3: Commit**

```bash
git add web/src/types/charging.ts
git commit -m "feat(web): add charging types"
```

---

### Task 8: Frontend `ChargingService`

**Files:**
- Create: `web/src/services/charging-service.ts`

**Interfaces:**
- Consumes: `ApiService` (existing, `services/api-service.ts`), `TenantService` (existing, `services/tenant-service.ts`), types from Task 7.
- Produces: `ChargingService` singleton with `getSettings()`, `putSettings(settings)`, `getSummary(from, to)`, `getVehicleSessions(tokenId, from, to)`, `exportCsv(from, to, tokenId?)`. Tasks 10–11 consume this.

- [ ] **Step 1: Write the service**

```typescript
// web/src/services/charging-service.ts
import { ApiService } from './api-service.ts';
import { TenantService } from './tenant-service.ts';
import { ChargingFleetSummary, ChargingSettings } from '../types/charging.ts';

export class ChargingService {
    private static instance: ChargingService;
    public static getInstance(): ChargingService {
        if (!ChargingService.instance) {
            ChargingService.instance = new ChargingService();
        }
        return ChargingService.instance;
    }

    /** GET /charging/settings. */
    getSettings(): Promise<ChargingSettings> {
        return ApiService.getInstance().get<ChargingSettings>('/charging/settings');
    }

    /** PUT /charging/settings. */
    putSettings(settings: ChargingSettings): Promise<ChargingSettings> {
        return ApiService.getInstance().put<ChargingSettings>('/charging/settings', settings);
    }

    /** GET /charging/summary?from&to. Fleet-wide rollup + map points. */
    getSummary(from: Date, to: Date): Promise<ChargingFleetSummary> {
        const q = new URLSearchParams({ from: from.toISOString(), to: to.toISOString() });
        return ApiService.getInstance().get<ChargingFleetSummary>(`/charging/summary?${q.toString()}`);
    }

    /** GET /charging/:tokenId/sessions?from&to. */
    getVehicleSessions(tokenId: number, from: Date, to: Date): Promise<{ sessions: ChargingFleetSummary['sessions'] }> {
        const q = new URLSearchParams({ from: from.toISOString(), to: to.toISOString() });
        return ApiService.getInstance().get(`/charging/${tokenId}/sessions?${q.toString()}`);
    }

    /** Trigger a browser download of the CSV export. Omit tokenId for the fleet-wide export. */
    async exportCsv(from: Date, to: Date, tokenId?: number): Promise<void> {
        const base = ApiService.getInstance().getApiBaseUrl();
        const token = localStorage.getItem('token');
        const q = new URLSearchParams({ from: from.toISOString(), to: to.toISOString() });
        if (tokenId) q.set('tokenId', String(tokenId));
        const res = await fetch(`${base}/charging/export.csv?${q.toString()}`, {
            headers: {
                ...(token ? { Authorization: `Bearer ${token}` } : {}),
                ...TenantService.getInstance().tenantIdHeader(),
            },
        });
        if (!res.ok) {
            throw new Error(`export failed: ${res.status} ${await res.text()}`);
        }
        const blob = await res.blob();
        const disposition = res.headers.get('Content-Disposition') || '';
        const match = /filename="?([^";]+)"?/i.exec(disposition);
        const filename = match?.[1] || 'charging-export.csv';
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
    }
}
```

- [ ] **Step 2: Verify the type-checker passes**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/services/charging-service.ts
git commit -m "feat(web): add ChargingService API client"
```

---

### Task 9: Frontend `ChargingCache`

**Files:**
- Create: `web/src/services/charging-cache.ts`

**Interfaces:**
- Consumes: `ChargingFleetSummary` (Task 7).
- Produces: `ChargingCache` with `get(tenantId)`, `set(tenantId, data)`, `invalidate()`. Task 11 consumes this.

- [ ] **Step 1: Write the cache**

```typescript
// web/src/services/charging-cache.ts
import { ChargingFleetSummary } from '../types/charging.ts';

/**
 * Holds the last-loaded charging fleet summary so navigating away to a
 * vehicle drilldown and back — or leaving the tab and returning — doesn't
 * re-trigger the full loading state and the fleet's worth of telemetry
 * round trips. Mirrors TCOCache's pattern.
 *
 * Keyed by tenant id so switching tenants doesn't serve the previous
 * tenant's cached data. No TTL — served until explicitly invalidated (a
 * settings save, or the view's own manual refresh).
 */
let cached: { tenantId: string; data: ChargingFleetSummary } | null = null;

export const ChargingCache = {
    get(tenantId: string): ChargingFleetSummary | null {
        return cached && cached.tenantId === tenantId ? cached.data : null;
    },
    set(tenantId: string, data: ChargingFleetSummary): void {
        cached = { tenantId, data };
    },
    invalidate(): void {
        cached = null;
    },
};
```

- [ ] **Step 2: Verify the type-checker passes**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/services/charging-cache.ts
git commit -m "feat(web): add ChargingCache"
```

---

### Task 10: `charging-view.ts` + nav wiring

**Files:**
- Create: `web/src/views/charging-view.ts`
- Modify: `web/src/views/index.ts`
- Modify: `web/src/elements/app-root.ts`
- Modify: `web/src/elements/side-nav.ts`

**Interfaces:**
- Consumes: `ChargingService` (Task 8), `ChargingCache` (Task 9), types (Task 7), `createFleetMap`/`applyTileTheme` (existing, `utils/fleet-map.ts`), `themeService` (existing, `services/theme-service.ts`), `sharedStyles` (existing, `global-styles.ts`).
- Produces: `<charging-view>` custom element, registered in the router and side nav.

- [ ] **Step 1: Write the view**

```typescript
// web/src/views/charging-view.ts
import { LitElement, html, css, nothing } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import L from 'leaflet';
import 'leaflet.markercluster';
import { sharedStyles } from '../global-styles.ts';
import { themeService } from '../services/theme-service.ts';
import { ChargingCache } from '../services/charging-cache.ts';
import { ChargingService } from '../services/charging-service.ts';
import { ChargingFleetSummary, ChargingSettings, ChargingSessionView } from '../types/charging.ts';
import { createFleetMap, applyTileTheme } from '../utils/fleet-map.ts';

function formatMoney(n?: number): string {
    if (n == null) return '—';
    return n.toLocaleString(undefined, { style: 'currency', currency: 'USD' });
}

function formatKwh(n?: number): string {
    if (n == null) return '—';
    return `${n.toLocaleString(undefined, { maximumFractionDigits: 1 })} kWh`;
}

const WINDOW_DAYS = 30;

@customElement('charging-view')
export class ChargingView extends LitElement {
    @property({ type: String }) tenantId = '';

    @state() private loading = true;
    @state() private error = '';
    @state() private summary: ChargingFleetSummary | null = null;
    @state() private settings: ChargingSettings = { currency: 'USD' };
    @state() private savingSettings = false;
    @state() private settingsError = '';
    @state() private exporting = false;
    @state() private selectedTokenId: number | null = null;

    private leafletMap: L.Map | null = null;
    private markers: L.MarkerClusterGroup | null = null;
    private unsubscribeTheme: (() => void) | null = null;

    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex;
                flex-direction: column;
                width: 100%;
                height: 100%;
                overflow-y: auto;
                background: var(--background);
            }
            header.top-bar {
                position: sticky;
                top: 0;
                z-index: 40;
                display: flex;
                align-items: center;
                justify-content: space-between;
                height: var(--top-bar-height, 80px);
                padding: 0 var(--gutter);
                background: var(--background);
                border-bottom: 1px solid var(--border-color);
            }
            .totals {
                display: flex;
                gap: 24px;
                padding: 16px var(--gutter);
            }
            .totals .stat { display: flex; flex-direction: column; }
            .totals .stat .value { font-size: 1.4rem; font-weight: 600; }
            .totals .stat .label { font-size: 0.8rem; color: var(--text-secondary); }
            #charging-map { height: 360px; margin: 0 var(--gutter); border-radius: 8px; }
            table { width: 100%; border-collapse: collapse; margin-top: 16px; }
            th, td { text-align: left; padding: 8px 16px; border-bottom: 1px solid var(--border-color); }
            .settings-panel { display: flex; gap: 12px; flex-wrap: wrap; padding: 16px var(--gutter); align-items: flex-end; }
            .settings-panel label { display: flex; flex-direction: column; font-size: 0.8rem; gap: 4px; }
            .error { color: var(--error-color, #d33); padding: 0 var(--gutter); }
        `,
    ];

    connectedCallback(): void {
        super.connectedCallback();
        this.unsubscribeTheme = themeService.subscribe(() => {
            if (this.leafletMap) applyTileTheme(this.leafletMap, themeService.current());
        });
        this.load();
    }

    disconnectedCallback(): void {
        super.disconnectedCallback();
        this.unsubscribeTheme?.();
        this.leafletMap?.remove();
        this.leafletMap = null;
    }

    private windowRange(): { from: Date; to: Date } {
        const to = new Date();
        const from = new Date(to.getTime() - WINDOW_DAYS * 24 * 60 * 60 * 1000);
        return { from, to };
    }

    private async load(): Promise<void> {
        const cached = ChargingCache.get(this.tenantId);
        if (cached) {
            this.summary = cached;
            this.loading = false;
        }
        this.settings = await ChargingService.getInstance().getSettings().catch(() => this.settings);
        try {
            const { from, to } = this.windowRange();
            const summary = await ChargingService.getInstance().getSummary(from, to);
            this.summary = summary;
            ChargingCache.set(this.tenantId, summary);
        } catch (e) {
            this.error = e instanceof Error ? e.message : String(e);
        } finally {
            this.loading = false;
        }
    }

    protected updated(): void {
        const el = this.renderRoot.querySelector('#charging-map') as HTMLElement | null;
        if (el && !this.leafletMap) {
            this.leafletMap = createFleetMap(el, { zoomControl: true });
            applyTileTheme(this.leafletMap, themeService.current());
            this.markers = L.markerClusterGroup();
            this.leafletMap.addLayer(this.markers);
        }
        this.renderMarkers();
    }

    private renderMarkers(): void {
        if (!this.markers) return;
        this.markers.clearLayers();
        for (const s of this.summary?.sessions ?? []) {
            if (s.lat == null || s.lng == null) continue;
            const marker = L.circleMarker([s.lat, s.lng], {
                radius: 6, fillColor: '#69dbad', color: '#fff', weight: 1.5, fillOpacity: 0.85,
            });
            marker.bindPopup(
                `${s.vehicleLabel}<br>${formatKwh(s.addedEnergyKwh)}<br>${formatMoney(s.cost)} spent / ${formatMoney(s.savings)} saved`,
            );
            marker.on('click', () => { this.selectedTokenId = s.tokenId; });
            this.markers.addLayer(marker);
        }
    }

    private async saveSettings(e: Event): Promise<void> {
        e.preventDefault();
        this.savingSettings = true;
        this.settingsError = '';
        try {
            this.settings = await ChargingService.getInstance().putSettings(this.settings);
            ChargingCache.invalidate();
            await this.load();
        } catch (err) {
            this.settingsError = err instanceof Error ? err.message : String(err);
        } finally {
            this.savingSettings = false;
        }
    }

    private updateSetting(key: keyof ChargingSettings, value: string): void {
        const n = value === '' ? undefined : Number(value);
        this.settings = { ...this.settings, [key]: n };
    }

    private async exportCsv(): Promise<void> {
        this.exporting = true;
        try {
            const { from, to } = this.windowRange();
            await ChargingService.getInstance().exportCsv(from, to, this.selectedTokenId ?? undefined);
        } catch (e) {
            this.error = e instanceof Error ? e.message : String(e);
        } finally {
            this.exporting = false;
        }
    }

    render() {
        const fleet = this.summary?.fleet;
        const sessions = this.selectedTokenId
            ? (this.summary?.sessions ?? []).filter((s) => s.tokenId === this.selectedTokenId)
            : (this.summary?.sessions ?? []);
        return html`
            <header class="top-bar">
                <h1>${msg('Charging')}</h1>
                <button ?disabled=${this.exporting} @click=${() => this.exportCsv()}>
                    ${msg('Export CSV')}
                </button>
            </header>
            ${this.error ? html`<p class="error">${this.error}</p>` : nothing}
            <div class="totals">
                <div class="stat">
                    <span class="value">${formatKwh(fleet?.addedEnergyKwh)}</span>
                    <span class="label">${msg('Energy added')}</span>
                </div>
                <div class="stat">
                    <span class="value">${formatMoney(fleet?.cost)}</span>
                    <span class="label">${msg('Spent on electricity')}</span>
                </div>
                <div class="stat">
                    <span class="value">${formatMoney(fleet?.savings)}</span>
                    <span class="label">${msg('Saved vs. gasoline')}</span>
                </div>
            </div>
            <div id="charging-map"></div>
            <form class="settings-panel" @submit=${(e: Event) => this.saveSettings(e)}>
                <label>
                    ${msg('Electricity rate ($/kWh)')}
                    <input type="number" step="0.01" .value=${this.settings.electricityRate?.toString() ?? ''}
                        @input=${(e: InputEvent) => this.updateSetting('electricityRate', (e.target as HTMLInputElement).value)} />
                </label>
                <label>
                    ${msg('Gas price ($/gallon)')}
                    <input type="number" step="0.01" .value=${this.settings.gasPrice?.toString() ?? ''}
                        @input=${(e: InputEvent) => this.updateSetting('gasPrice', (e.target as HTMLInputElement).value)} />
                </label>
                <label>
                    ${msg('Gas MPG equivalent')}
                    <input type="number" step="1" .value=${this.settings.gasMpgEquivalent?.toString() ?? ''}
                        @input=${(e: InputEvent) => this.updateSetting('gasMpgEquivalent', (e.target as HTMLInputElement).value)} />
                </label>
                <label>
                    ${msg('Vehicle efficiency (kWh/mile)')}
                    <input type="number" step="0.01" .value=${this.settings.vehicleKwhPerMile?.toString() ?? ''}
                        @input=${(e: InputEvent) => this.updateSetting('vehicleKwhPerMile', (e.target as HTMLInputElement).value)} />
                </label>
                <button type="submit" ?disabled=${this.savingSettings}>${msg('Save settings')}</button>
                ${this.settingsError ? html`<span class="error">${this.settingsError}</span>` : nothing}
            </form>
            ${this.selectedTokenId
                ? html`<button @click=${() => { this.selectedTokenId = null; }}>${msg('Show all vehicles')}</button>`
                : nothing}
            ${this.loading
                ? html`<p>${msg('Loading…')}</p>`
                : html`
                    <table>
                        <thead>
                            <tr>
                                <th>${msg('Vehicle')}</th>
                                <th>${msg('Started')}</th>
                                <th>${msg('Energy')}</th>
                                <th>${msg('Cost')}</th>
                                <th>${msg('Saved')}</th>
                            </tr>
                        </thead>
                        <tbody>
                            ${sessions.map(
                                (s: ChargingSessionView) => html`
                                    <tr>
                                        <td>${s.vehicleLabel}</td>
                                        <td>${new Date(s.startedAt).toLocaleString()}</td>
                                        <td>${formatKwh(s.addedEnergyKwh)}</td>
                                        <td>${formatMoney(s.cost)}</td>
                                        <td>${formatMoney(s.savings)}</td>
                                    </tr>
                                `,
                            )}
                        </tbody>
                    </table>
                `}
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'charging-view': ChargingView;
    }
}
```

- [ ] **Step 2: Register the view in `web/src/views/index.ts`**

Add alongside the existing `export * from './tco-view.ts';`:

```typescript
export * from './charging-view.ts';
```

- [ ] **Step 3: Wire the route and nav key in `web/src/elements/app-root.ts`**

Add `'charging'` to the `NavKey` union (next to `'tco'`), import the view (`import '../views/charging-view.ts';` alongside the existing `import '../views/tco-view.ts';`), add a route entry next to the TCO route:

```typescript
            { path: '/:tenantId/charging',            render: () => html`<charging-view .tenantId=${this.tenantId}></charging-view>` },
```

And add a matching branch to the path-to-nav-key resolver next to `if (path.startsWith('/tco')) return 'tco';`:

```typescript
        if (path.startsWith('/charging')) return 'charging';
```

- [ ] **Step 4: Add the nav item in `web/src/elements/side-nav.ts`**

Add `'charging'` to the `NavKey` union, and a new entry to `ITEMS` next to the `tco` entry:

```typescript
    { key: 'charging', icon: 'ev_station',    label: () => msg('Charging'), suffix: '/charging' },
```

- [ ] **Step 5: Verify the frontend builds**

Run: `cd web && npx tsc && npx vite build`
Expected: no type errors, build succeeds.

- [ ] **Step 6: Commit**

```bash
git add web/src/views/charging-view.ts web/src/views/index.ts web/src/elements/app-root.ts web/src/elements/side-nav.ts
git commit -m "feat(web): add Charging tab (map, sessions table, settings panel)"
```

---

### Task 11: Localization sync

**Files:**
- Modify: `web/xliff/*.xlf` (generated)
- Modify: `web/src/generated/*` (generated)

**Interfaces:**
- Consumes: every `msg(...)` call added in Task 10.
- Produces: no new interfaces — this task only regenerates localization artifacts so `localize:check` (CI) doesn't fail on the new strings.

- [ ] **Step 1: Regenerate localization files**

Run: `cd web && npm run localize`
Expected: extracts the new `msg()` strings from `charging-view.ts` into `xliff/*.xlf` and regenerates `src/generated/*`, exits 0.

- [ ] **Step 2: Verify the localization check passes**

Run: `cd web && npm run localize:check`
Expected: exit 0 (no diff between committed and regenerated localization files).

- [ ] **Step 3: Commit**

```bash
git add web/xliff web/src/generated
git commit -m "chore(web): sync localization for Charging tab strings"
```

---

### Task 12: Manual verification

**Files:** none (verification only).

**Interfaces:** none.

- [ ] **Step 1: Start the backend and frontend dev servers**

Run: `cd api && make migrate && go run ./cmd/fleet-lite-app` (backend), and in a second terminal `cd web && npm run dev` (frontend).

- [ ] **Step 2: Configure tenant charging settings**

In Chrome, open the app, navigate to the new "Charging" tab in the side nav, fill in the settings panel (electricity rate, gas price, gas MPG equivalent, vehicle kWh/mile), and save. Confirm the form reloads with the saved values (a page refresh should still show them — i.e. `GET /charging/settings` round-trips correctly).

- [ ] **Step 3: Verify sessions against a charging-capable vehicle**

Using a DIMO vehicle simulator or a real EV connection with charging history (see the `dimo` skill's Vehicle Simulator reference for spinning one up), confirm: the fleet totals bar shows nonzero kWh/cost/savings, at least one map marker appears at the vehicle's charging location, and the sessions table lists rows whose kWh/cost/savings agree with the map popup.

- [ ] **Step 4: Verify CSV export**

Click "Export CSV" with no vehicle selected (fleet-wide) and confirm the downloaded file opens with one row per session and the same totals as the UI. Click a map marker to filter to one vehicle, export again, and confirm the single-vehicle CSV only contains that vehicle's rows.

- [ ] **Step 5: Verify a vehicle with no charging signals is excluded, not zeroed**

Open the Charging tab for a tenant that includes a combustion-engine vehicle. Confirm it contributes no rows to the sessions table and isn't counted in fleet totals (rather than appearing with 0 kWh).
