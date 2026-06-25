package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/aarondl/sqlboiler/v4/types"
	"github.com/lib/pq"
	"github.com/rs/zerolog"
)

// Geofence assignment scopes — which vehicles a geofence applies to. See
// docs/GEOFENCES_PLAN.md Decision 3.
const (
	GeofenceScopeAll    = "all"    // every vehicle in the tenant (no per-vehicle rows)
	GeofenceScopeGroup  = "group"  // vehicles in any of group_ids (via vehicle_fleet_groups)
	GeofenceScopeManual = "manual" // vehicles explicitly listed in vehicle_geofences
)

var (
	// ErrGeofenceNotFound is returned when a geofence id does not exist for the tenant.
	ErrGeofenceNotFound = errors.New("geofence not found")
	// ErrGeofenceNameExists is returned on a (tenant_id, name) unique collision.
	ErrGeofenceNameExists = errors.New("geofence with this name already exists")
	// ErrInvalidScope is returned when scope is not one of all|group|manual.
	ErrInvalidScope = errors.New("invalid geofence scope")
	// ErrInvalidGeometry is returned when geometry is not a valid GeoJSON Polygon.
	ErrInvalidGeometry = errors.New("geometry must be a valid GeoJSON Polygon")
	// ErrUnknownGroup is returned when a group-scoped geofence references a group
	// id that does not belong to the tenant.
	ErrUnknownGroup = errors.New("geofence references a group that does not exist")
	// ErrGroupScopeNeedsGroups is returned when scope=group but no group ids are given.
	ErrGroupScopeNeedsGroups = errors.New("group-scoped geofence requires at least one group id")
)

// GeofenceInput is the payload for creating a geofence.
type GeofenceInput struct {
	Name          string
	Color         string
	Geometry      json.RawMessage // GeoJSON Polygon
	SpeedLimitKPH *int            // nil = no limit
	Scope         string          // defaults to "all" when empty
	GroupIDs      []string        // required when scope = group
}

// GeofencePatch is the partial-update payload. nil/zero fields are left
// unchanged; a non-nil GroupIDs replaces the set; a non-nil SpeedLimitKPH sets
// the limit (clearing to NULL is deferred — see docs/GEOFENCES_PLAN.md).
type GeofencePatch struct {
	Name          *string
	Color         *string
	Geometry      json.RawMessage
	SpeedLimitKPH *int
	Scope         *string
	GroupIDs      []string
}

// GeofenceWithCount is a geofence plus the count of vehicles it currently
// resolves to (across its scope), for the management UI.
type GeofenceWithCount struct {
	Geofence     *dbmodels.Geofence
	VehicleCount int
}

// GeofenceService owns tenant-scoped CRUD over geofences and their manual
// vehicle assignments, plus scope resolution (all|group|manual → token ids).
// Pure data access — write-path attestation is a later phase (see the plan).
type GeofenceService struct {
	logger *zerolog.Logger
	pdb    *db.Store
}

func NewGeofenceService(logger *zerolog.Logger, pdb *db.Store) *GeofenceService {
	return &GeofenceService{logger: logger, pdb: pdb}
}

func validScope(s string) bool {
	switch s {
	case GeofenceScopeAll, GeofenceScopeGroup, GeofenceScopeManual:
		return true
	default:
		return false
	}
}

// ListGeofences returns the tenant's geofences with effective vehicle counts,
// ordered by name.
func (s *GeofenceService) ListGeofences(ctx context.Context, tenantID string) ([]GeofenceWithCount, error) {
	rows, err := dbmodels.Geofences(
		dbmodels.GeofenceWhere.TenantID.EQ(tenantID),
		qm.OrderBy("name"),
	).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, fmt.Errorf("list geofences: %w", err)
	}
	out := make([]GeofenceWithCount, len(rows))
	for i, g := range rows {
		count, cerr := s.vehicleCount(ctx, tenantID, g)
		if cerr != nil {
			s.logger.Warn().Err(cerr).Str("geofence", g.ID).Msg("count geofence vehicles")
		}
		out[i] = GeofenceWithCount{Geofence: g, VehicleCount: count}
	}
	return out, nil
}

// GetGeofence returns one geofence for the tenant, or ErrGeofenceNotFound.
func (s *GeofenceService) GetGeofence(ctx context.Context, tenantID, id string) (*dbmodels.Geofence, error) {
	g, err := dbmodels.Geofences(
		dbmodels.GeofenceWhere.ID.EQ(id),
		dbmodels.GeofenceWhere.TenantID.EQ(tenantID),
	).One(ctx, s.pdb.DBS().Reader)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGeofenceNotFound
		}
		return nil, fmt.Errorf("get geofence: %w", err)
	}
	return g, nil
}

// CreateGeofence creates a tenant-scoped geofence with a slug id derived from
// the name. Validates scope/groups/geometry and computes the geodesic area.
func (s *GeofenceService) CreateGeofence(ctx context.Context, tenantID, createdBy string, in GeofenceInput) (*dbmodels.Geofence, error) {
	scope := in.Scope
	if scope == "" {
		scope = GeofenceScopeAll
	}
	if !validScope(scope) {
		return nil, ErrInvalidScope
	}

	groupIDs, err := s.normalizeGroups(ctx, tenantID, scope, in.GroupIDs)
	if err != nil {
		return nil, err
	}

	area, err := polygonAreaM2(in.Geometry)
	if err != nil {
		return nil, err
	}

	id := slug(in.Name)
	if id == "" {
		return nil, fmt.Errorf("geofence name must contain at least one alphanumeric character")
	}

	g := &dbmodels.Geofence{
		ID:            id,
		TenantID:      tenantID,
		Name:          in.Name,
		Color:         in.Color,
		Geometry:      types.JSON(in.Geometry),
		AreaM2:        area,
		SpeedLimitKPH: null.IntFromPtr(in.SpeedLimitKPH),
		Scope:         scope,
		GroupIds:      types.StringArray(groupIDs),
		CreatedBy:     createdBy,
	}
	if err := g.Insert(ctx, s.pdb.DBS().Writer, boil.Infer()); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrGeofenceNameExists
		}
		return nil, fmt.Errorf("insert geofence: %w", err)
	}
	return g, nil
}

// UpdateGeofence applies a partial update. Recomputes area when geometry
// changes; re-validates groups when scope/groups change; drops stale manual
// assignments when scope moves away from manual.
func (s *GeofenceService) UpdateGeofence(ctx context.Context, tenantID, id string, p GeofencePatch) (*dbmodels.Geofence, error) {
	g, err := s.GetGeofence(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	cols := []string{"updated_at"}

	if p.Name != nil && *p.Name != "" {
		g.Name = *p.Name
		cols = append(cols, "name")
	}
	if p.Color != nil && *p.Color != "" {
		g.Color = *p.Color
		cols = append(cols, "color")
	}
	if p.Geometry != nil {
		area, aerr := polygonAreaM2(p.Geometry)
		if aerr != nil {
			return nil, aerr
		}
		g.Geometry = types.JSON(p.Geometry)
		g.AreaM2 = area
		cols = append(cols, "geometry", "area_m2")
	}
	if p.SpeedLimitKPH != nil {
		g.SpeedLimitKPH = null.IntFrom(*p.SpeedLimitKPH)
		cols = append(cols, "speed_limit_kph")
	}

	// Scope + groups: resolve the target scope, then validate/normalize groups
	// against it. group_ids is cleared whenever scope is not "group".
	newScope := g.Scope
	if p.Scope != nil && *p.Scope != "" {
		newScope = *p.Scope
		if !validScope(newScope) {
			return nil, ErrInvalidScope
		}
	}
	newGroups := []string(g.GroupIds)
	if p.GroupIDs != nil {
		newGroups = p.GroupIDs
	}
	normGroups, err := s.normalizeGroups(ctx, tenantID, newScope, newGroups)
	if err != nil {
		return nil, err
	}
	if newScope != g.Scope {
		g.Scope = newScope
		cols = append(cols, "scope")
	}
	g.GroupIds = types.StringArray(normGroups)
	cols = appendUnique(cols, "group_ids")

	if _, err := g.Update(ctx, s.pdb.DBS().Writer, boil.Whitelist(cols...)); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrGeofenceNameExists
		}
		return nil, fmt.Errorf("update geofence: %w", err)
	}

	// Manual assignments only matter while scope is manual; clean them up
	// otherwise so EffectiveTokenIDs and counts stay unambiguous.
	if g.Scope != GeofenceScopeManual {
		if _, derr := dbmodels.VehicleGeofences(
			dbmodels.VehicleGeofenceWhere.TenantID.EQ(tenantID),
			dbmodels.VehicleGeofenceWhere.GeofenceID.EQ(g.ID),
		).DeleteAll(ctx, s.pdb.DBS().Writer); derr != nil {
			s.logger.Warn().Err(derr).Str("geofence", g.ID).Msg("drop stale manual assignments")
		}
	}
	return g, nil
}

// DeleteGeofence deletes a geofence (cascading its manual assignments).
func (s *GeofenceService) DeleteGeofence(ctx context.Context, tenantID, id string) error {
	g, err := s.GetGeofence(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if _, err := g.Delete(ctx, s.pdb.DBS().Writer); err != nil {
		return fmt.Errorf("delete geofence: %w", err)
	}
	return nil
}

// AddVehicle assigns a vehicle to a manual-scope geofence, idempotently. Returns
// whether a new assignment row was created. Verifies the geofence is manual and
// both geofence and vehicle belong to the tenant.
func (s *GeofenceService) AddVehicle(ctx context.Context, tenantID string, tokenID int64, geofenceID string) (bool, error) {
	g, err := s.GetGeofence(ctx, tenantID, geofenceID)
	if err != nil {
		return false, err
	}
	if g.Scope != GeofenceScopeManual {
		return false, ErrInvalidScope
	}
	ok, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleWhere.TokenID.EQ(tokenID),
	).Exists(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return false, fmt.Errorf("verify vehicle: %w", err)
	}
	if !ok {
		return false, ErrVehicleNotFound
	}

	exists, err := dbmodels.VehicleGeofences(
		dbmodels.VehicleGeofenceWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleGeofenceWhere.TokenID.EQ(tokenID),
		dbmodels.VehicleGeofenceWhere.GeofenceID.EQ(geofenceID),
	).Exists(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return false, fmt.Errorf("check assignment: %w", err)
	}
	if exists {
		return false, nil
	}

	m := &dbmodels.VehicleGeofence{TenantID: tenantID, TokenID: tokenID, GeofenceID: geofenceID}
	if err := m.Insert(ctx, s.pdb.DBS().Writer, boil.Infer()); err != nil {
		return false, fmt.Errorf("assign vehicle to geofence: %w", err)
	}
	return true, nil
}

// RemoveVehicle unassigns a vehicle from a manual-scope geofence. Returns
// whether a row was removed.
func (s *GeofenceService) RemoveVehicle(ctx context.Context, tenantID string, tokenID int64, geofenceID string) (bool, error) {
	if _, err := s.GetGeofence(ctx, tenantID, geofenceID); err != nil {
		return false, err
	}
	n, err := dbmodels.VehicleGeofences(
		dbmodels.VehicleGeofenceWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleGeofenceWhere.TokenID.EQ(tokenID),
		dbmodels.VehicleGeofenceWhere.GeofenceID.EQ(geofenceID),
	).DeleteAll(ctx, s.pdb.DBS().Writer)
	if err != nil {
		return false, fmt.Errorf("unassign vehicle from geofence: %w", err)
	}
	return n > 0, nil
}

// EffectiveTokenIDs resolves a geofence's scope to the set of vehicle token ids
// it currently applies to (tenant-scoped). Shared by the management UI counts
// and the future write-path attestation fan-out.
func (s *GeofenceService) EffectiveTokenIDs(ctx context.Context, tenantID string, g *dbmodels.Geofence) ([]int64, error) {
	var q string
	var args []interface{}
	switch g.Scope {
	case GeofenceScopeManual:
		q = `SELECT token_id FROM vehicle_geofences WHERE tenant_id = $1 AND geofence_id = $2 ORDER BY token_id`
		args = []interface{}{tenantID, g.ID}
	case GeofenceScopeGroup:
		if len(g.GroupIds) == 0 {
			return []int64{}, nil
		}
		q = `SELECT DISTINCT token_id FROM vehicle_fleet_groups
		     WHERE tenant_id = $1 AND fleet_group_id = ANY($2) ORDER BY token_id`
		args = []interface{}{tenantID, pq.Array([]string(g.GroupIds))}
	default: // all
		q = `SELECT token_id FROM vehicles WHERE tenant_id = $1 ORDER BY token_id`
		args = []interface{}{tenantID}
	}
	var rows []struct {
		TokenID int64 `boil:"token_id"`
	}
	if err := queries.Raw(q, args...).Bind(ctx, s.pdb.DBS().Reader, &rows); err != nil {
		return nil, fmt.Errorf("effective token ids: %w", err)
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.TokenID
	}
	return ids, nil
}

// vehicleCount returns the effective vehicle count for a geofence without
// materialising the id list (cheaper for the "all" scope on large fleets).
func (s *GeofenceService) vehicleCount(ctx context.Context, tenantID string, g *dbmodels.Geofence) (int, error) {
	switch g.Scope {
	case GeofenceScopeManual:
		n, err := dbmodels.VehicleGeofences(
			dbmodels.VehicleGeofenceWhere.TenantID.EQ(tenantID),
			dbmodels.VehicleGeofenceWhere.GeofenceID.EQ(g.ID),
		).Count(ctx, s.pdb.DBS().Reader)
		return int(n), err
	case GeofenceScopeGroup:
		if len(g.GroupIds) == 0 {
			return 0, nil
		}
		var row struct {
			C int `boil:"c"`
		}
		err := queries.Raw(
			`SELECT COUNT(DISTINCT token_id) AS c FROM vehicle_fleet_groups
			 WHERE tenant_id = $1 AND fleet_group_id = ANY($2)`,
			tenantID, pq.Array([]string(g.GroupIds)),
		).Bind(ctx, s.pdb.DBS().Reader, &row)
		return row.C, err
	default: // all
		n, err := dbmodels.Vehicles(dbmodels.VehicleWhere.TenantID.EQ(tenantID)).Count(ctx, s.pdb.DBS().Reader)
		return int(n), err
	}
}

// normalizeGroups validates and normalizes group ids against a scope: for the
// group scope they must be non-empty and all belong to the tenant; for any
// other scope the list is cleared to nil.
func (s *GeofenceService) normalizeGroups(ctx context.Context, tenantID, scope string, groupIDs []string) ([]string, error) {
	if scope != GeofenceScopeGroup {
		// Non-nil empty slice: the group_ids column is NOT NULL, and a nil
		// types.StringArray marshals to SQL NULL (a nil slice violates the
		// constraint on UPDATE). "{}" is the correct empty value.
		return []string{}, nil
	}
	uniq := dedupeNonEmpty(groupIDs)
	if len(uniq) == 0 {
		return nil, ErrGroupScopeNeedsGroups
	}
	n, err := dbmodels.FleetGroups(
		dbmodels.FleetGroupWhere.TenantID.EQ(tenantID),
		dbmodels.FleetGroupWhere.ID.IN(uniq),
	).Count(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, fmt.Errorf("validate geofence groups: %w", err)
	}
	if int(n) != len(uniq) {
		return nil, ErrUnknownGroup
	}
	return uniq, nil
}

// polygonAreaM2 parses a GeoJSON Polygon and returns its geodesic area in m²
// (outer ring minus holes). Returns ErrInvalidGeometry for anything that isn't a
// well-formed Polygon.
func polygonAreaM2(geometry json.RawMessage) (float64, error) {
	if len(geometry) == 0 {
		return 0, ErrInvalidGeometry
	}
	var poly struct {
		Type        string        `json:"type"`
		Coordinates [][][]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(geometry, &poly); err != nil {
		return 0, ErrInvalidGeometry
	}
	if poly.Type != "Polygon" || len(poly.Coordinates) == 0 {
		return 0, ErrInvalidGeometry
	}
	outer := poly.Coordinates[0]
	// A closed ring needs at least 4 positions (first == last), each [lon, lat].
	if len(outer) < 4 {
		return 0, ErrInvalidGeometry
	}
	for _, ring := range poly.Coordinates {
		for _, pt := range ring {
			if len(pt) < 2 {
				return 0, ErrInvalidGeometry
			}
		}
	}
	area := ringAreaM2(outer)
	for _, hole := range poly.Coordinates[1:] {
		area -= ringAreaM2(hole)
	}
	if area < 0 {
		area = -area
	}
	return area, nil
}

const earthRadiusM = 6378137.0

// ringAreaM2 is the spherical-excess area of a single ring (positions as
// [lon, lat] degrees), in m². Sign depends on winding; callers take abs.
func ringAreaM2(ring [][]float64) float64 {
	n := len(ring)
	if n < 3 {
		return 0
	}
	var total float64
	for i := 0; i < n; i++ {
		p1 := ring[i]
		p2 := ring[(i+1)%n]
		total += rad(p2[0]-p1[0]) * (2 + math.Sin(rad(p1[1])) + math.Sin(rad(p2[1])))
	}
	return total * earthRadiusM * earthRadiusM / 2.0
}

func rad(deg float64) float64 { return deg * math.Pi / 180.0 }

func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func appendUnique(cols []string, col string) []string {
	for _, c := range cols {
		if c == col {
			return cols
		}
	}
	return append(cols, col)
}
