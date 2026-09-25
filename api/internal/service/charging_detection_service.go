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
// computing only the gaps not already covered by charging_scan_coverage.
func (s *ChargingDetectionService) Sessions(ctx context.Context, tenant models.Tenant, tokenID int64, from, to time.Time) ([]dbmodels.ChargingSession, error) {
	gaps, err := s.uncoveredGaps(ctx, tenant.ID, tokenID, from, to)
	if err != nil {
		return nil, err
	}
	// Coverage ends where the first still-running session begins; see settledUntil.
	coveredTo := to
	for _, gap := range gaps {
		segments, serr := s.telemetry.RechargeSegments(tenant, uint64(tokenID), rfc3339(gap.from), rfc3339(gap.to))
		if serr != nil {
			return nil, fmt.Errorf("recharge segments: %w", serr)
		}
		// Logged even on the empty-result happy path — a vehicle that never
		// charges (or whose connection doesn't report the recharge mechanism's
		// signals) previously left zero trace anywhere in the logs, making it
		// indistinguishable from "never queried at all" during investigation.
		s.logger.Info().Int64("tokenID", tokenID).Str("from", rfc3339(gap.from)).Str("to", rfc3339(gap.to)).
			Int("segments", len(segments)).Msg("recharge segments fetched")
		if perr := s.persistSessions(ctx, tenant.ID, tokenID, segments, gap.from, gap.to); perr != nil {
			return nil, perr
		}
		if settled := settledUntil(segments, gap.to); settled.Before(coveredTo) {
			coveredTo = settled
		}
	}
	if len(gaps) > 0 && coveredTo.After(from) {
		if cerr := s.mergeCoverage(ctx, tenant.ID, tokenID, from, coveredTo); cerr != nil {
			return nil, cerr
		}
	}
	return s.readSessions(ctx, tenant.ID, tokenID, from, to)
}

// coverageIntervals loads this vehicle's existing scanned ranges, coalesced.
func (s *ChargingDetectionService) coverageIntervals(ctx context.Context, tenantID string, tokenID int64) ([]timeInterval, error) {
	rows, err := dbmodels.ChargingScanCoverages(
		dbmodels.ChargingScanCoverageWhere.TenantID.EQ(tenantID),
		dbmodels.ChargingScanCoverageWhere.TokenID.EQ(tokenID),
	).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, fmt.Errorf("load charging scan coverage: %w", err)
	}
	ivs := make([]timeInterval, len(rows))
	for i, r := range rows {
		ivs[i] = timeInterval{r.ScannedFrom, r.ScannedTo}
	}
	return coalesce(ivs), nil
}

// uncoveredGaps returns the sub-ranges of [from, to] not yet scanned.
func (s *ChargingDetectionService) uncoveredGaps(ctx context.Context, tenantID string, tokenID int64, from, to time.Time) ([]timeInterval, error) {
	existing, err := s.coverageIntervals(ctx, tenantID, tokenID)
	if err != nil {
		return nil, err
	}
	return computeGaps(existing, from, to), nil
}

// mergeCoverage records [from, to] as scanned, coalescing with existing
// intervals so the coverage table stays a minimal set of disjoint ranges —
// mirrors GeofenceDetectionService.mergeCoverage.
func (s *ChargingDetectionService) mergeCoverage(ctx context.Context, tenantID string, tokenID int64, from, to time.Time) error {
	existing, err := s.coverageIntervals(ctx, tenantID, tokenID)
	if err != nil {
		return err
	}
	merged := coalesce(append(existing, timeInterval{from, to}))
	writer := s.pdb.DBS().Writer
	if _, err := dbmodels.ChargingScanCoverages(
		dbmodels.ChargingScanCoverageWhere.TenantID.EQ(tenantID),
		dbmodels.ChargingScanCoverageWhere.TokenID.EQ(tokenID),
	).DeleteAll(ctx, writer); err != nil {
		return fmt.Errorf("clear charging coverage: %w", err)
	}
	for _, iv := range merged {
		cov := &dbmodels.ChargingScanCoverage{TenantID: tenantID, TokenID: tokenID, ScannedFrom: iv.from, ScannedTo: iv.to}
		if err := cov.Insert(ctx, writer, boil.Infer()); err != nil {
			return fmt.Errorf("insert charging coverage: %w", err)
		}
	}
	return nil
}

// persistSessions runs the detection sweep over samples for one gap and
// replaces any previously-detected sessions in that gap with the fresh set —
// a re-scan of an overlapping window can yield sessions with slightly
// shifted started_at (telemetry-api buckets are window-relative), so
// deleting the window first keeps exactly one copy and makes recompute
// idempotent. Mirrors GeofenceDetectionService.persistPasses.
func (s *ChargingDetectionService) persistSessions(ctx context.Context, tenantID string, tokenID int64, segments []Segment, from, to time.Time) error {
	var detected []detectedChargingSession
	for _, seg := range segments {
		// Still charging: no real end yet. Left out until it finishes — the
		// coverage stops short of it, so a later request picks it up.
		if seg.IsOngoing {
			continue
		}
		d := sessionFromSegment(seg)
		if d.isNoise() {
			continue
		}
		detected = append(detected, d)
	}

	writer := s.pdb.DBS().Writer
	if _, err := dbmodels.ChargingSessions(
		dbmodels.ChargingSessionWhere.TenantID.EQ(tenantID),
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
			AvgPowerKW:     null.Float64FromPtr(d.avgPowerKw),
			SocStartPCT:    null.Float64FromPtr(d.socStart),
			SocEndPCT:      null.Float64FromPtr(d.socEnd),
			Lat:            null.Float64FromPtr(d.lat),
			LNG:            null.Float64FromPtr(d.lng),
		}
		if err := m.Insert(ctx, writer, boil.Infer()); err != nil {
			return fmt.Errorf("insert charging session: %w", err)
		}
	}
	return nil
}

// readSessions reads persisted sessions for one vehicle overlapping [from, to].
func (s *ChargingDetectionService) readSessions(ctx context.Context, tenantID string, tokenID int64, from, to time.Time) ([]dbmodels.ChargingSession, error) {
	rows, err := dbmodels.ChargingSessions(
		dbmodels.ChargingSessionWhere.TenantID.EQ(tenantID),
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
