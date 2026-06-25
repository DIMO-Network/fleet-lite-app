package service

import (
	"context"
	"encoding/json"
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

// geofenceSampleInterval is the telemetry bucketing used for detection. 30s
// matches the trip-replay cadence; speed-exceeded and enter/exit timestamps
// resolve to this granularity (a vehicle can clip a small geofence between
// buckets — a documented limitation; tighten per-query if needed).
const geofenceSampleInterval = "30s"

// PassSummary is one cached geofence pass, shaped for the API. speedExceeded is
// computed at read time against the geofence's CURRENT speed limit (never cached
// — the limit can change on edit while the raw maxSpeedKph cannot).
type PassSummary struct {
	EnteredAt     time.Time `json:"enteredAt"`
	ExitedAt      time.Time `json:"exitedAt"`
	DwellS        int       `json:"dwellS"`
	MaxSpeedKph   *float64  `json:"maxSpeedKph,omitempty"`
	SpeedExceeded bool      `json:"speedExceeded"`
	EntryLat      float64   `json:"entryLat"`
	EntryLng      float64   `json:"entryLng"`
	ExitLat       float64   `json:"exitLat"`
	ExitLng       float64   `json:"exitLng"`
	MaxSpeedLat   *float64  `json:"maxSpeedLat,omitempty"`
	MaxSpeedLng   *float64  `json:"maxSpeedLng,omitempty"`
	NumSamples    int       `json:"numSamples"`
}

// GeofenceCrossing is one geofence a trip/window touched, with its passes.
type GeofenceCrossing struct {
	GeofenceID    string        `json:"geofenceId"`
	Name          string        `json:"name"`
	Color         string        `json:"color"`
	SpeedLimitKph *int          `json:"speedLimitKph,omitempty"`
	Passes        []PassSummary `json:"passes"`
}

// detectedPass is the internal result of the polygon sweep before persistence.
type detectedPass struct {
	enteredAt   time.Time
	exitedAt    time.Time
	maxSpeed    *float64
	entryLat    float64
	entryLng    float64
	exitLat     float64
	exitLng     float64
	maxSpeedLat *float64
	maxSpeedLng *float64
	numSamples  int
}

// GeofenceDetectionService computes which geofences a vehicle's telemetry passed
// through, on demand, and caches the summary results. Past telemetry is
// immutable and geofence geometry is fixed per id, so a computed pass never goes
// stale; a scan-coverage ledger prevents recomputation. See docs/GEOFENCES_PLAN.md.
type GeofenceDetectionService struct {
	logger    *zerolog.Logger
	pdb       *db.Store
	telemetry TelemetryAPIService
	geofences *GeofenceService
}

func NewGeofenceDetectionService(logger *zerolog.Logger, pdb *db.Store, telemetry TelemetryAPIService, geofences *GeofenceService) *GeofenceDetectionService {
	return &GeofenceDetectionService{logger: logger, pdb: pdb, telemetry: telemetry, geofences: geofences}
}

// TripGeofences returns the geofences a single vehicle's telemetry crossed in
// [from, to] — entry point 1 (the trip panel). It computes only the geofences
// whose [from, to] is not already covered, fetching telemetry once and caching
// the resulting passes, then reads back all passes overlapping the window.
func (s *GeofenceDetectionService) TripGeofences(ctx context.Context, tenant models.Tenant, tokenID int64, from, to time.Time) ([]GeofenceCrossing, error) {
	// Tenant-scope the token: refuse to fetch telemetry for a vehicle that isn't
	// the tenant's (passes also surface only for this tenant's geofences, but the
	// telemetry fetch itself must be gated).
	ok, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenant.ID),
		dbmodels.VehicleWhere.TokenID.EQ(tokenID),
	).Exists(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, fmt.Errorf("verify vehicle: %w", err)
	}
	if !ok {
		return nil, ErrVehicleNotFound
	}

	fences, err := dbmodels.Geofences(
		dbmodels.GeofenceWhere.TenantID.EQ(tenant.ID),
		qm.OrderBy("name"),
	).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, fmt.Errorf("load geofences: %w", err)
	}
	if len(fences) == 0 {
		return []GeofenceCrossing{}, nil
	}

	// Which geofences still need computing for this window?
	uncovered, err := s.uncoveredGeofences(ctx, tokenID, fences, from, to)
	if err != nil {
		return nil, err
	}

	if len(uncovered) > 0 {
		// One telemetry fetch serves every uncovered geofence — the samples are
		// the same; only the polygon test differs.
		samples, serr := s.telemetry.GeofenceSamples(tenant, uint64(tokenID), rfc3339(from), rfc3339(to), geofenceSampleInterval)
		if serr != nil {
			return nil, fmt.Errorf("geofence samples: %w", serr)
		}
		for _, g := range uncovered {
			if cerr := s.computeAndCache(ctx, tenant.ID, tokenID, g, samples, from, to); cerr != nil {
				s.logger.Warn().Err(cerr).Str("geofence", g.ID).Int64("tokenID", tokenID).Msg("compute geofence passes")
			}
		}
	}

	return s.readCrossings(ctx, tokenID, fences, from, to)
}

// uncoveredGeofences returns the geofences whose [from,to] is not fully inside an
// existing scan-coverage row for this vehicle.
func (s *GeofenceDetectionService) uncoveredGeofences(ctx context.Context, tokenID int64, fences dbmodels.GeofenceSlice, from, to time.Time) (dbmodels.GeofenceSlice, error) {
	ids := make([]string, len(fences))
	for i, g := range fences {
		ids[i] = g.ID
	}
	cov, err := dbmodels.GeofenceScanCoverages(
		dbmodels.GeofenceScanCoverageWhere.TokenID.EQ(tokenID),
		dbmodels.GeofenceScanCoverageWhere.GeofenceID.IN(ids),
	).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, fmt.Errorf("load scan coverage: %w", err)
	}
	covered := make(map[string]bool, len(fences))
	for _, c := range cov {
		// A window is covered when an existing scan fully contains it.
		if !c.ScannedFrom.After(from) && !c.ScannedTo.Before(to) {
			covered[c.GeofenceID] = true
		}
	}
	out := make(dbmodels.GeofenceSlice, 0, len(fences))
	for _, g := range fences {
		if !covered[g.ID] {
			out = append(out, g)
		}
	}
	return out, nil
}

// computeAndCache runs the polygon sweep for one geofence over the samples,
// upserts the detected passes, and records the scanned window so it is not
// recomputed.
func (s *GeofenceDetectionService) computeAndCache(ctx context.Context, tenantID string, tokenID int64, g *dbmodels.Geofence, samples []GeoSample, from, to time.Time) error {
	rings, err := parsePolygonRings(json.RawMessage(g.Geometry))
	if err != nil {
		return fmt.Errorf("parse geometry: %w", err)
	}
	passes := detectPasses(samples, rings)

	writer := s.pdb.DBS().Writer
	// Clear any previously-detected passes in this scanned window before inserting
	// the fresh set. telemetry-api buckets are window-relative, so a re-scan of an
	// overlapping (not identical) window can yield passes with slightly shifted
	// entered_at — deleting the window first keeps exactly one copy and makes
	// recompute idempotent. (Identical windows are short-circuited upstream by the
	// coverage check, so this only runs on a genuine recompute.)
	if _, err := dbmodels.GeofencePasses(
		dbmodels.GeofencePassWhere.GeofenceID.EQ(g.ID),
		dbmodels.GeofencePassWhere.TokenID.EQ(tokenID),
		qm.Where("entered_at >= ? AND entered_at <= ?", from, to),
	).DeleteAll(ctx, writer); err != nil {
		return fmt.Errorf("clear stale passes: %w", err)
	}
	for _, p := range passes {
		m := &dbmodels.GeofencePass{
			GeofenceID:  g.ID,
			TenantID:    tenantID,
			TokenID:     tokenID,
			EnteredAt:   p.enteredAt,
			ExitedAt:    p.exitedAt,
			DwellS:      int(p.exitedAt.Sub(p.enteredAt).Seconds()),
			MaxSpeedKPH: null.Float64FromPtr(p.maxSpeed),
			EntryLat:    p.entryLat,
			EntryLNG:    p.entryLng,
			ExitLat:     p.exitLat,
			ExitLNG:     p.exitLng,
			MaxSpeedLat: null.Float64FromPtr(p.maxSpeedLat),
			MaxSpeedLNG: null.Float64FromPtr(p.maxSpeedLng),
			NumSamples:  p.numSamples,
		}
		if err := m.Insert(ctx, writer, boil.Infer()); err != nil {
			return fmt.Errorf("insert pass: %w", err)
		}
	}

	cov := &dbmodels.GeofenceScanCoverage{
		GeofenceID:  g.ID,
		TenantID:    tenantID,
		TokenID:     tokenID,
		ScannedFrom: from,
		ScannedTo:   to,
	}
	// Same window re-scanned → keep the row; widen scanned_to if this scan ran later.
	if err := cov.Upsert(ctx, writer, true, []string{"geofence_id", "token_id", "scanned_from"}, boil.Whitelist("scanned_to"), boil.Infer()); err != nil {
		return fmt.Errorf("upsert coverage: %w", err)
	}
	return nil
}

// readCrossings reads cached passes for all the geofences that overlap [from,to]
// and assembles the API result, computing speedExceeded against each geofence's
// current speed limit.
func (s *GeofenceDetectionService) readCrossings(ctx context.Context, tokenID int64, fences dbmodels.GeofenceSlice, from, to time.Time) ([]GeofenceCrossing, error) {
	ids := make([]string, len(fences))
	for i, g := range fences {
		ids[i] = g.ID
	}
	rows, err := dbmodels.GeofencePasses(
		dbmodels.GeofencePassWhere.TokenID.EQ(tokenID),
		dbmodels.GeofencePassWhere.GeofenceID.IN(ids),
		qm.Where("entered_at <= ? AND exited_at >= ?", to, from),
		qm.OrderBy("entered_at"),
	).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, fmt.Errorf("read passes: %w", err)
	}

	byFence := make(map[string][]PassSummary, len(fences))
	for _, r := range rows {
		byFence[r.GeofenceID] = append(byFence[r.GeofenceID], toPassSummary(r))
	}

	out := make([]GeofenceCrossing, 0, len(byFence))
	for _, g := range fences {
		ps := byFence[g.ID]
		if len(ps) == 0 {
			continue
		}
		var limit *int
		if g.SpeedLimitKPH.Valid {
			v := g.SpeedLimitKPH.Int
			limit = &v
		}
		for i := range ps {
			ps[i].SpeedExceeded = limit != nil && ps[i].MaxSpeedKph != nil && *ps[i].MaxSpeedKph > float64(*limit)
		}
		out = append(out, GeofenceCrossing{
			GeofenceID:    g.ID,
			Name:          g.Name,
			Color:         g.Color,
			SpeedLimitKph: limit,
			Passes:        ps,
		})
	}
	return out, nil
}

func toPassSummary(r *dbmodels.GeofencePass) PassSummary {
	return PassSummary{
		EnteredAt:   r.EnteredAt,
		ExitedAt:    r.ExitedAt,
		DwellS:      r.DwellS,
		MaxSpeedKph: r.MaxSpeedKPH.Ptr(),
		EntryLat:    r.EntryLat,
		EntryLng:    r.EntryLNG,
		ExitLat:     r.ExitLat,
		ExitLng:     r.ExitLNG,
		MaxSpeedLat: r.MaxSpeedLat.Ptr(),
		MaxSpeedLng: r.MaxSpeedLNG.Ptr(),
		NumSamples:  r.NumSamples,
	}
}

// detectPasses sweeps ordered samples and emits one pass per maximal run of
// consecutive inside-the-polygon samples. enter/exit timestamps come from the
// run's edge samples; max speed (and its coords) is the worst sample in the run.
func detectPasses(samples []GeoSample, rings [][][]float64) []detectedPass {
	var passes []detectedPass
	var cur *detectedPass
	for _, smp := range samples {
		inside := pointInPolygon(smp.Lat, smp.Lng, rings)
		if inside {
			if cur == nil {
				cur = &detectedPass{
					enteredAt: smp.Time,
					entryLat:  smp.Lat,
					entryLng:  smp.Lng,
				}
			}
			cur.exitedAt = smp.Time
			cur.exitLat = smp.Lat
			cur.exitLng = smp.Lng
			cur.numSamples++
			if smp.SpeedKph != nil && (cur.maxSpeed == nil || *smp.SpeedKph > *cur.maxSpeed) {
				sp := *smp.SpeedKph
				lat, lng := smp.Lat, smp.Lng
				cur.maxSpeed = &sp
				cur.maxSpeedLat = &lat
				cur.maxSpeedLng = &lng
			}
		} else if cur != nil {
			passes = append(passes, *cur)
			cur = nil
		}
	}
	if cur != nil {
		passes = append(passes, *cur)
	}
	return passes
}

// parsePolygonRings extracts a GeoJSON Polygon's rings as [][lon,lat] arrays
// (outer ring first, then holes). Mirrors the validation in polygonAreaM2.
func parsePolygonRings(geometry json.RawMessage) ([][][]float64, error) {
	if len(geometry) == 0 {
		return nil, ErrInvalidGeometry
	}
	var poly struct {
		Type        string        `json:"type"`
		Coordinates [][][]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(geometry, &poly); err != nil {
		return nil, ErrInvalidGeometry
	}
	if poly.Type != "Polygon" || len(poly.Coordinates) == 0 || len(poly.Coordinates[0]) < 4 {
		return nil, ErrInvalidGeometry
	}
	return poly.Coordinates, nil
}

// pointInPolygon reports whether (lat, lng) is inside the polygon: inside the
// outer ring and outside every hole. Ring positions are [lon, lat]. Standard
// ray-casting; good enough at fleet scale (geofences are small relative to the
// globe, so planar treatment of a degree grid is acceptable).
func pointInPolygon(lat, lng float64, rings [][][]float64) bool {
	if len(rings) == 0 || !rayCast(lng, lat, rings[0]) {
		return false
	}
	for _, hole := range rings[1:] {
		if rayCast(lng, lat, hole) {
			return false
		}
	}
	return true
}

// rayCast returns true when (x, y) is inside the ring (positions [x=lon, y=lat]).
func rayCast(x, y float64, ring [][]float64) bool {
	inside := false
	n := len(ring)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		xi, yi := ring[i][0], ring[i][1]
		xj, yj := ring[j][0], ring[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			inside = !inside
		}
	}
	return inside
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }
