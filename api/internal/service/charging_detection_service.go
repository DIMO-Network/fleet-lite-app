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
			AddedEnergyKWH: null.Float64FromPtr(d.addedEnergyKwh()),
			AvgPowerKW:     null.Float64FromPtr(d.avgPowerKw()),
			SocStartPCT:    null.Float64FromPtr(d.socStart),
			SocEndPCT:      null.Float64FromPtr(d.socEnd),
			Lat:            null.Float64FromPtr(d.lat),
			LNG:            null.Float64FromPtr(d.lng),
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
