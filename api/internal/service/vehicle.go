package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/lib/pq"
	"github.com/rs/zerolog"
)

// AccessibleTokenIDs returns the set of vehicle token ids a limited member may
// touch (union of their allowed groups). Callers with unrestricted access
// should not call this — pass-through their full sets instead.
func (s *VehicleService) AccessibleTokenIDs(ctx context.Context, tenantID string, allowedGroupIDs []string) (map[int64]bool, error) {
	var rows []struct {
		TokenID int64 `boil:"token_id"`
	}
	if err := queries.Raw(
		`SELECT DISTINCT token_id FROM vehicle_fleet_groups WHERE tenant_id = $1 AND fleet_group_id = ANY($2)`,
		tenantID, pq.Array(allowedGroupIDs),
	).Bind(ctx, s.pdb.DBS().Reader, &rows); err != nil {
		return nil, fmt.Errorf("accessible token ids: %w", err)
	}
	out := make(map[int64]bool, len(rows))
	for _, r := range rows {
		out[r.TokenID] = true
	}
	return out, nil
}

// allowedGroupsFilter restricts a vehicles query to tokens inside any of the
// caller's allowed fleet groups. Only applied for limited members (non-nil
// allowedGroupIDs) — ungrouped vehicles are deliberately invisible to them.
// See docs/GROUP_ACCESS_PLAN.md.
func allowedGroupsFilter(tenantID string, allowedGroupIDs []string) qm.QueryMod {
	return qm.Where(
		"token_id IN (SELECT token_id FROM vehicle_fleet_groups WHERE tenant_id = ? AND fleet_group_id = ANY(?))",
		tenantID, pq.Array(allowedGroupIDs),
	)
}

// VehicleService syncs a tenant's privileged vehicles from identity-api into the
// DB and reads them back, scoped by tenant.
type VehicleService struct {
	logger      *zerolog.Logger
	pdb         *db.Store
	identityAPI gateway.IdentityAPI
}

func NewVehicleService(logger *zerolog.Logger, pdb *db.Store, identityAPI gateway.IdentityAPI) *VehicleService {
	return &VehicleService{logger: logger, pdb: pdb, identityAPI: identityAPI}
}

// SyncVehicles fetches the vehicles privileged to the tenant's developer-license
// client ID and upserts them into the vehicles table. Returns the count synced.
func (s *VehicleService) SyncVehicles(ctx context.Context, tenant *models.Tenant) (int, error) {
	if tenant.ClientID == "" {
		return 0, fmt.Errorf("tenant %s has no DIMO client ID", tenant.ID)
	}
	vehicles, err := s.identityAPI.FetchPrivilegedVehicles(tenant.ClientID)
	if err != nil {
		return 0, fmt.Errorf("fetch privileged vehicles: %w", err)
	}

	for _, v := range vehicles {
		raw, _ := json.Marshal(v)
		row := &dbmodels.Vehicle{
			TenantID:     tenant.ID,
			TokenID:      v.TokenID,
			OwnerAddress: null.StringFrom(v.Owner),
			Make:         null.StringFrom(v.Definition.Make),
			Model:        null.StringFrom(v.Definition.Model),
			Year:         null.IntFrom(v.Definition.Year),
			DefinitionID: null.StringFrom(v.Definition.ID),
			Raw:          null.JSONFrom(raw),
			SyncedAt:     time.Now(),
		}
		if v.AftermarketDevice != nil {
			row.DeviceType = null.StringFrom("aftermarket")
			row.Imei = null.StringFrom(v.AftermarketDevice.IMEI)
			row.Serial = null.StringFrom(v.AftermarketDevice.Serial)
		} else if v.SyntheticDevice.TokenID != 0 {
			row.DeviceType = null.StringFrom("synthetic")
		}
		if v.MintedAt != nil {
			row.MintedAt = null.TimeFrom(*v.MintedAt)
		}

		if err := row.Upsert(ctx, s.pdb.DBS().Writer, true,
			[]string{"tenant_id", "token_id"},
			boil.Whitelist("owner_address", "make", "model", "year", "definition_id",
				"device_type", "imei", "serial", "minted_at", "raw", "synced_at", "updated_at"),
			boil.Infer()); err != nil {
			return 0, fmt.Errorf("upsert vehicle %d: %w", v.TokenID, err)
		}
	}
	return len(vehicles), nil
}

// ListVehicles returns the tenant's synced vehicles in identity-api Vehicle
// shape, with IsFavorite populated from the tenant's favorites. A non-nil
// allowedGroupIDs limits the result to vehicles in those fleet groups (limited
// members); nil means unrestricted.
func (s *VehicleService) ListVehicles(ctx context.Context, tenantID string, allowedGroupIDs []string) ([]models.Vehicle, error) {
	mods := []qm.QueryMod{
		qm.Where("tenant_id = ?", tenantID),
		// Most-recently-seen first (the composite idx_vehicles_tenant_last_seen
		// serves this filter+sort); never-seen vehicles sort last, token_id as a
		// stable tiebreaker. Favourites are pinned to the top client-side.
		qm.OrderBy("last_seen DESC NULLS LAST, token_id"),
	}
	if allowedGroupIDs != nil {
		mods = append(mods, allowedGroupsFilter(tenantID, allowedGroupIDs))
	}
	rows, err := dbmodels.Vehicles(mods...).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, err
	}
	favorites, err := s.favoriteSet(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]models.Vehicle, 0, len(rows))
	for _, r := range rows {
		v := rowToVehicle(r)
		v.IsFavorite = favorites[v.TokenID]
		v.LicensePlate = r.LicensePlate.String
		v.VIN = r.Vin.String
		applyLastLocation(&v, r)
		out = append(out, v)
	}
	return out, nil
}

// GetVehicle returns one synced vehicle for the tenant, or an error if not
// found. A non-nil allowedGroupIDs additionally requires the vehicle to be in
// one of those groups — out-of-scope vehicles error exactly like nonexistent
// ones, so limited members can't probe what exists.
func (s *VehicleService) GetVehicle(ctx context.Context, tenantID string, tokenID int64, allowedGroupIDs []string) (*models.Vehicle, error) {
	mods := []qm.QueryMod{
		qm.Where("tenant_id = ?", tenantID),
		qm.And("token_id = ?", tokenID),
	}
	if allowedGroupIDs != nil {
		mods = append(mods, allowedGroupsFilter(tenantID, allowedGroupIDs))
	}
	r, err := dbmodels.Vehicles(mods...).One(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, err
	}
	v := rowToVehicle(r)
	v.IsFavorite, err = s.IsFavorite(ctx, tenantID, tokenID)
	if err != nil {
		return nil, err
	}
	v.LicensePlate = r.LicensePlate.String
	v.VIN = r.Vin.String
	applyLastLocation(&v, r)
	return &v, nil
}

// applyLastLocation copies the cached last-GPS-fix columns onto the assembled
// Vehicle. Kept off rowToVehicle because those columns aren't part of the
// identity `raw` shape — same reasoning as IsFavorite/LicensePlate/VIN.
func applyLastLocation(v *models.Vehicle, r *dbmodels.Vehicle) {
	if r.LastLat.Valid {
		lat := r.LastLat.Float64
		v.LastLat = &lat
	}
	if r.LastLon.Valid {
		lon := r.LastLon.Float64
		v.LastLon = &lon
	}
	if r.LastSeen.Valid {
		ts := r.LastSeen.Time
		v.LastSeen = &ts
	}
	if r.LocationPulledAt.Valid {
		ts := r.LocationPulledAt.Time
		v.LocationPulledAt = &ts
	}
}

// UpsertLastLocations writes through the latest GPS fix for each vehicle into
// its row (the last_lat/last_lon/last_seen display cache). Best-effort: a fix
// with no/unparseable timestamp is skipped so we never stamp last_seen with a
// zero time, and a per-row failure is logged but never fails the batch. Only
// the three location columns (plus updated_at) are touched; a vehicle missing
// from the table simply updates zero rows.
func (s *VehicleService) UpsertLastLocations(ctx context.Context, tenantID string, locs map[uint64]LocationCoords) {
	for id, c := range locs {
		ts, err := time.Parse(time.RFC3339, c.Timestamp)
		if err != nil {
			continue
		}
		row := &dbmodels.Vehicle{
			TenantID: tenantID,
			TokenID:  int64(id),
			LastLat:  null.Float64From(c.Lat),
			LastLon:  null.Float64From(c.Lon),
			LastSeen: null.TimeFrom(ts),
		}
		if _, err := row.Update(ctx, s.pdb.DBS().Writer,
			boil.Whitelist("last_lat", "last_lon", "last_seen", "updated_at")); err != nil {
			s.logger.Warn().Uint64("tokenId", id).Err(err).Msg("write-through last location")
		}
	}
}

// StampLocationPulled records that we just fetched these vehicles' locations
// from telemetry-api (location_pulled_at = now) so the freshness window can
// suppress re-pulls. Call with FleetLocationsResult.Fetched — the vehicles
// actually queried this call, regardless of outcome (coords / no-data /
// no-permission) — NOT cache hits, whose pulled_at must keep its earlier value.
// Best-effort: one batched UPDATE, logged-and-swallowed on failure.
func (s *VehicleService) StampLocationPulled(ctx context.Context, tenantID string, tokenIDs []uint64) {
	if len(tokenIDs) == 0 {
		return
	}
	ids := make([]int64, len(tokenIDs))
	for i, id := range tokenIDs {
		ids[i] = int64(id)
	}
	now := time.Now()
	if _, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleWhere.TokenID.IN(ids),
	).UpdateAll(ctx, s.pdb.DBS().Writer, dbmodels.M{
		"location_pulled_at": now,
		"updated_at":         now,
	}); err != nil {
		s.logger.Warn().Err(err).Str("tenant_id", tenantID).Msg("stamp location_pulled_at")
	}
}

// favoriteSet returns the tenant's favorited token IDs as a lookup set.
func (s *VehicleService) favoriteSet(ctx context.Context, tenantID string) (map[int64]bool, error) {
	rows, err := dbmodels.VehicleFavorites(
		qm.Where("tenant_id = ?", tenantID),
	).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(rows))
	for _, r := range rows {
		out[r.TokenID] = true
	}
	return out, nil
}

// IsFavorite reports whether the tenant has starred the given vehicle.
func (s *VehicleService) IsFavorite(ctx context.Context, tenantID string, tokenID int64) (bool, error) {
	return dbmodels.VehicleFavorites(
		qm.Where("tenant_id = ?", tenantID),
		qm.And("token_id = ?", tokenID),
	).Exists(ctx, s.pdb.DBS().Reader)
}

// AddFavorite stars a vehicle for the tenant. Idempotent — re-starring an
// already-favorited vehicle is a no-op.
func (s *VehicleService) AddFavorite(ctx context.Context, tenantID string, tokenID int64) error {
	row := &dbmodels.VehicleFavorite{TenantID: tenantID, TokenID: tokenID}
	return row.Upsert(ctx, s.pdb.DBS().Writer, false, []string{"tenant_id", "token_id"}, boil.None(), boil.Infer())
}

// RemoveFavorite unstars a vehicle for the tenant. Idempotent — unstarring a
// vehicle that isn't favorited is a no-op.
func (s *VehicleService) RemoveFavorite(ctx context.Context, tenantID string, tokenID int64) error {
	_, err := dbmodels.VehicleFavorites(
		qm.Where("tenant_id = ?", tenantID),
		qm.And("token_id = ?", tokenID),
	).DeleteAll(ctx, s.pdb.DBS().Writer)
	return err
}

// rowToVehicle reconstructs the identity Vehicle shape from a DB row, preferring
// the stored raw identity node and falling back to the flat columns.
func rowToVehicle(r *dbmodels.Vehicle) models.Vehicle {
	if r.Raw.Valid && len(r.Raw.JSON) > 0 {
		var v models.Vehicle
		if err := json.Unmarshal(r.Raw.JSON, &v); err == nil {
			return v
		}
	}
	return models.Vehicle{
		TokenID: r.TokenID,
		Owner:   r.OwnerAddress.String,
		Definition: models.Definition{
			ID:    r.DefinitionID.String,
			Make:  r.Make.String,
			Model: r.Model.String,
			Year:  r.Year.Int,
		},
	}
}
