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
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/rs/zerolog"
)

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
// shape, with IsFavorite populated from the tenant's favorites.
func (s *VehicleService) ListVehicles(ctx context.Context, tenantID string) ([]models.Vehicle, error) {
	rows, err := dbmodels.Vehicles(
		qm.Where("tenant_id = ?", tenantID),
		qm.OrderBy("token_id"),
	).All(ctx, s.pdb.DBS().Reader)
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
		out = append(out, v)
	}
	return out, nil
}

// GetVehicle returns one synced vehicle for the tenant, or nil if not found.
func (s *VehicleService) GetVehicle(ctx context.Context, tenantID string, tokenID int64) (*models.Vehicle, error) {
	r, err := dbmodels.Vehicles(
		qm.Where("tenant_id = ?", tenantID),
		qm.And("token_id = ?", tokenID),
	).One(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, err
	}
	v := rowToVehicle(r)
	v.IsFavorite, err = s.IsFavorite(ctx, tenantID, tokenID)
	if err != nil {
		return nil, err
	}
	v.LicensePlate = r.LicensePlate.String
	return &v, nil
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
