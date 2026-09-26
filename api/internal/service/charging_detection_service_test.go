package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chargeModel is a vehicle's charging as telemetry-api's recharge detector
// reports it: each true session appears clipped to what has been read so far
// (never past `now`, never outside the query window) with isOngoing=false, the
// way the real detector reports a charge still under way.
type chargeModel struct {
	TelemetryAPIService
	mu       sync.Mutex
	now      time.Time
	sessions []timeInterval
	queries  int
}

func (m *chargeModel) RechargeSegments(_ models.Tenant, _ uint64, from, to string) ([]Segment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queries++
	qFrom, _ := time.Parse(time.RFC3339, from)
	qTo, _ := time.Parse(time.RFC3339, to)
	var out []Segment
	for _, s := range m.sessions {
		start, end := s.from, s.to
		if start.Before(qFrom) {
			start = qFrom
		}
		if end.After(m.now) {
			end = m.now
		}
		if end.After(qTo) {
			end = qTo
		}
		if !start.Before(end) {
			continue
		}
		out = append(out, segment(rfc3339(start), rfc3339(end), 0, 0, f64ptr(10), f64ptr(30), nil, nil, nil))
	}
	return out, nil
}

func (m *chargeModel) setNow(t time.Time) {
	m.mu.Lock()
	m.now = t
	m.mu.Unlock()
}

type chargingFixture struct {
	svc    *ChargingDetectionService
	model  *chargeModel
	tenant models.Tenant
	store  *db.Store
}

func newChargingFixture(t *testing.T, sessions ...timeInterval) *chargingFixture {
	t.Helper()
	store := migratedStore(t)
	logger := zerolog.Nop()
	model := &chargeModel{sessions: sessions}
	svc := NewChargingDetectionService(&logger, store, model)
	svc.now = func() time.Time {
		model.mu.Lock()
		defer model.mu.Unlock()
		return model.now
	}
	tenant := models.Tenant{ID: uuid.NewString()}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = dbmodels.ChargingSessions(dbmodels.ChargingSessionWhere.TenantID.EQ(tenant.ID)).DeleteAll(ctx, store.DBS().Writer)
		_, _ = dbmodels.ChargingScanCoverages(dbmodels.ChargingScanCoverageWhere.TenantID.EQ(tenant.ID)).DeleteAll(ctx, store.DBS().Writer)
	})
	return &chargingFixture{svc: svc, model: model, tenant: tenant, store: store}
}

func (f *chargingFixture) load(t *testing.T, now time.Time, window time.Duration) []ChargingSessionRow {
	t.Helper()
	f.model.setNow(now)
	rows, err := f.svc.Sessions(context.Background(), f.tenant, 190171, now.Add(-window), now)
	require.NoError(t, err)
	return rows
}

func (f *chargingFixture) stored(t *testing.T) []*dbmodels.ChargingSession {
	t.Helper()
	rows, err := dbmodels.ChargingSessions(dbmodels.ChargingSessionWhere.TenantID.EQ(f.tenant.ID)).All(context.Background(), f.store.DBS().Reader)
	require.NoError(t, err)
	return rows
}

func mustTime(t *testing.T, ts string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, ts)
	require.NoError(t, err)
	return v
}

// The Charging tab opened mid-charge used to store the charge with a fake end
// and mark its window scanned; the next load then stored the rest as a second
// session. Now: shown as in progress, stored once with its real end.
func TestChargingSessions_ChargeInProgressIsStoredOnceWhenItFinishes(t *testing.T) {
	charge := timeInterval{mustTime(t, "2026-09-25T10:00:00Z"), mustTime(t, "2026-09-25T14:00:00Z")}
	f := newChargingFixture(t, charge)

	rows := f.load(t, mustTime(t, "2026-09-25T11:00:00Z"), 30*24*time.Hour)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].InProgress, "a charge still under way is in progress, not ended")
	assert.True(t, rows[0].StartedAt.Equal(charge.from))
	assert.Empty(t, f.stored(t), "nothing is stored while it may still grow")

	rows = f.load(t, mustTime(t, "2026-09-25T13:00:00Z"), 30*24*time.Hour)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].InProgress)
	assert.Empty(t, f.stored(t))

	rows = f.load(t, mustTime(t, "2026-09-25T17:00:00Z"), 30*24*time.Hour)
	require.Len(t, rows, 1)
	assert.False(t, rows[0].InProgress, "two hours after its last reading it is final")
	assert.True(t, rows[0].StartedAt.Equal(charge.from))
	assert.True(t, rows[0].EndedAt.Equal(charge.to), "the real end, not the reading at the first load")
	require.Len(t, f.stored(t), 1)

	rows = f.load(t, mustTime(t, "2026-09-26T09:00:00Z"), 30*24*time.Hour)
	require.Len(t, rows, 1, "later loads neither split nor duplicate it")
	require.Len(t, f.stored(t), 1)
}

// Coverage is recorded per gap: a session in progress at the trailing edge no
// longer stops a fully-scanned historical gap from being recorded.
func TestChargingSessions_InProgressSessionDoesNotHoldBackOtherGaps(t *testing.T) {
	now := mustTime(t, "2026-09-25T12:00:00Z")
	live := timeInterval{mustTime(t, "2026-09-25T08:00:00Z"), mustTime(t, "2026-09-25T20:00:00Z")}
	f := newChargingFixture(t, live)

	// Something already scanned the middle of the month.
	f.model.setNow(now)
	_, err := f.svc.Sessions(context.Background(), f.tenant, 190171,
		mustTime(t, "2026-09-10T00:00:00Z"), mustTime(t, "2026-09-20T00:00:00Z"))
	require.NoError(t, err)

	f.load(t, now, 30*24*time.Hour)
	cov, err := f.svc.coverageIntervals(context.Background(), f.store.DBS().Reader, f.tenant.ID, 190171)
	require.NoError(t, err)
	require.Len(t, cov, 1, "the leading gap, the middle and the settled part of the trailing gap form one range")
	assert.True(t, cov[0].from.Equal(now.Add(-30*24*time.Hour)))
	assert.True(t, cov[0].to.Equal(live.from), "coverage stops where the live charge began")

	queries := f.model.queries
	f.load(t, now.Add(time.Minute), 30*24*time.Hour)
	assert.Equal(t, queries+1, f.model.queries, "one query for the trailing gap, not a re-scan of the month")
}

// Several requests for one vehicle at once (fleet summary, the vehicle's tab, a
// CSV export) used to collide on the primary keys and drop the vehicle.
func TestChargingSessions_ConcurrentLoadsDoNotCollide(t *testing.T) {
	var sessions []timeInterval
	base := mustTime(t, "2026-09-01T06:00:00Z")
	for day := 0; day < 20; day++ {
		start := base.AddDate(0, 0, day)
		sessions = append(sessions, timeInterval{start, start.Add(90 * time.Minute)})
	}
	f := newChargingFixture(t, sessions...)
	f.model.setNow(mustTime(t, "2026-09-25T12:00:00Z"))

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.svc.Sessions(context.Background(), f.tenant, 190171,
				mustTime(t, "2026-08-26T12:00:00Z"), mustTime(t, "2026-09-25T12:00:00Z")); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(fmt.Errorf("concurrent load failed: %w", err))
	}
	assert.Len(t, f.stored(t), 20, "exactly one copy of each session")
}
