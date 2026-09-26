package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
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
//
// Only settled sessions are stored (see settleGap). A session that may still
// grow is reported as in progress, computed fresh on each request.
type ChargingDetectionService struct {
	logger    *zerolog.Logger
	pdb       *db.Store
	telemetry TelemetryAPIService
	now       func() time.Time
}

func NewChargingDetectionService(logger *zerolog.Logger, pdb *db.Store, telemetry TelemetryAPIService) *ChargingDetectionService {
	return &ChargingDetectionService{logger: logger, pdb: pdb, telemetry: telemetry, now: time.Now}
}

// ChargingSessionRow is one session as the detection service reports it.
type ChargingSessionRow struct {
	dbmodels.ChargingSession
	// InProgress means the session's last reading is recent enough that it may
	// still grow (see rechargeSettleMargin). Its EndedAt is that last reading,
	// not an end. Never stored.
	InProgress bool
}

// Sessions returns a vehicle's charging sessions starting in [from, to],
// computing only the gaps not already covered by charging_scan_coverage.
func (s *ChargingDetectionService) Sessions(ctx context.Context, tenant models.Tenant, tokenID int64, from, to time.Time) ([]ChargingSessionRow, error) {
	gaps, err := s.uncoveredGaps(ctx, tenant.ID, tokenID, from, to)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var live []ChargingSessionRow
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

		final, inProgress, coveredTo := settleGap(sessionsFromSegments(segments), gap, now)
		if err := s.recordGap(ctx, tenant.ID, tokenID, gap, withoutNoise(final), coveredTo); err != nil {
			return nil, err
		}
		for _, d := range inProgress {
			// No end yet (isOngoing): its latest reading is now, as far as
			// anything shown or priced is concerned.
			if d.endedAt.IsZero() {
				d.endedAt = now
			}
			if d.isNoise() {
				continue
			}
			live = append(live, ChargingSessionRow{ChargingSession: sessionRecord(tenant.ID, tokenID, d), InProgress: true})
		}
	}

	stored, err := s.readSessions(ctx, tenant.ID, tokenID, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]ChargingSessionRow, 0, len(stored)+len(live))
	for _, r := range stored {
		out = append(out, ChargingSessionRow{ChargingSession: r})
	}
	for _, r := range live {
		if !r.StartedAt.Before(from) && !r.StartedAt.After(to) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

// sessionsFromSegments maps telemetry-api segments to sessions, dropping any
// whose start can't be parsed: without a start there is nothing to key it on.
func sessionsFromSegments(segments []Segment) []detectedChargingSession {
	out := make([]detectedChargingSession, 0, len(segments))
	for _, seg := range segments {
		d := sessionFromSegment(seg)
		if d.startedAt.IsZero() {
			continue
		}
		out = append(out, d)
	}
	return out
}

// withoutNoise drops connector self-checks and relay blips (see isNoise).
func withoutNoise(sessions []detectedChargingSession) []detectedChargingSession {
	out := make([]detectedChargingSession, 0, len(sessions))
	for _, d := range sessions {
		if !d.isNoise() {
			out = append(out, d)
		}
	}
	return out
}

func sessionRecord(tenantID string, tokenID int64, d detectedChargingSession) dbmodels.ChargingSession {
	return dbmodels.ChargingSession{
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
}

// recordGap stores one gap's settled sessions and marks [gap.from, coveredTo]
// scanned, in one transaction.
//
// Serialized per vehicle with an advisory lock: the fleet summary, a vehicle's
// own tab and a CSV export can scan the same vehicle at once, and interleaved
// delete-then-insert runs collided on the primary keys, which dropped that
// vehicle from the fleet summary. Coverage is recorded per gap, so a session
// still in progress in one gap no longer holds back coverage of another.
func (s *ChargingDetectionService) recordGap(ctx context.Context, tenantID string, tokenID int64, gap timeInterval, final []detectedChargingSession, coveredTo time.Time) error {
	tx, err := s.pdb.DBS().Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin charging scan: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit

	lockKey := "charging-scan:" + tenantID + ":" + strconv.FormatInt(tokenID, 10)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock charging scan: %w", err)
	}
	if err := s.persistSessions(ctx, tx, tenantID, tokenID, final, gap.from, gap.to); err != nil {
		return err
	}
	if coveredTo.After(gap.from) {
		if err := s.mergeCoverage(ctx, tx, tenantID, tokenID, gap.from, coveredTo); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit charging scan: %w", err)
	}
	return nil
}

// coverageIntervals loads this vehicle's existing scanned ranges, coalesced.
func (s *ChargingDetectionService) coverageIntervals(ctx context.Context, exec boil.ContextExecutor, tenantID string, tokenID int64) ([]timeInterval, error) {
	rows, err := dbmodels.ChargingScanCoverages(
		dbmodels.ChargingScanCoverageWhere.TenantID.EQ(tenantID),
		dbmodels.ChargingScanCoverageWhere.TokenID.EQ(tokenID),
	).All(ctx, exec)
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
	existing, err := s.coverageIntervals(ctx, s.pdb.DBS().Reader, tenantID, tokenID)
	if err != nil {
		return nil, err
	}
	return computeGaps(existing, from, to), nil
}

// mergeCoverage records [from, to] as scanned, coalescing with existing
// intervals so the coverage table stays a minimal set of disjoint ranges —
// mirrors GeofenceDetectionService.mergeCoverage.
func (s *ChargingDetectionService) mergeCoverage(ctx context.Context, exec boil.ContextExecutor, tenantID string, tokenID int64, from, to time.Time) error {
	existing, err := s.coverageIntervals(ctx, exec, tenantID, tokenID)
	if err != nil {
		return err
	}
	merged := coalesce(append(existing, timeInterval{from, to}))
	if _, err := dbmodels.ChargingScanCoverages(
		dbmodels.ChargingScanCoverageWhere.TenantID.EQ(tenantID),
		dbmodels.ChargingScanCoverageWhere.TokenID.EQ(tokenID),
	).DeleteAll(ctx, exec); err != nil {
		return fmt.Errorf("clear charging coverage: %w", err)
	}
	for _, iv := range merged {
		cov := &dbmodels.ChargingScanCoverage{TenantID: tenantID, TokenID: tokenID, ScannedFrom: iv.from, ScannedTo: iv.to}
		if err := cov.Insert(ctx, exec, boil.Infer()); err != nil {
			return fmt.Errorf("insert charging coverage: %w", err)
		}
	}
	return nil
}

// persistSessions replaces any previously-detected sessions in one gap with
// the fresh settled set — a re-scan of an overlapping window can yield
// sessions with slightly shifted started_at (telemetry-api buckets are
// window-relative), so deleting the window first keeps exactly one copy and
// makes recompute idempotent. Mirrors GeofenceDetectionService.persistPasses.
func (s *ChargingDetectionService) persistSessions(ctx context.Context, exec boil.ContextExecutor, tenantID string, tokenID int64, sessions []detectedChargingSession, from, to time.Time) error {
	if _, err := dbmodels.ChargingSessions(
		dbmodels.ChargingSessionWhere.TenantID.EQ(tenantID),
		dbmodels.ChargingSessionWhere.TokenID.EQ(tokenID),
		qm.Where("started_at >= ? AND started_at <= ?", from, to),
	).DeleteAll(ctx, exec); err != nil {
		return fmt.Errorf("clear stale charging sessions: %w", err)
	}
	for _, d := range sessions {
		m := sessionRecord(tenantID, tokenID, d)
		if err := m.Insert(ctx, exec, boil.Infer()); err != nil {
			return fmt.Errorf("insert charging session: %w", err)
		}
	}
	return nil
}

// readSessions reads persisted sessions for one vehicle starting in [from, to].
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
