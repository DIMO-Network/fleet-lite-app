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
	if f, ok := decimalFloat(row.GasMPGEquivalent); ok {
		out.GasMpgEquivalent = &f
	}
	if f, ok := decimalFloat(row.VehicleKWHPerMile); ok {
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
		row.GasMPGEquivalent = types.NewNullDecimal(new(decimal.Big).SetFloat64(*in.GasMpgEquivalent))
	}
	if in.VehicleKwhPerMile != nil {
		row.VehicleKWHPerMile = types.NewNullDecimal(new(decimal.Big).SetFloat64(*in.VehicleKwhPerMile))
	}
	now := time.Now()
	row.CreatedAt = now
	row.UpdatedAt = now
	return row.Upsert(ctx, s.pdb.DBS().Writer, true,
		[]string{dbmodels.TenantChargingSettingColumns.TenantID},
		boil.Whitelist(
			dbmodels.TenantChargingSettingColumns.ElectricityRate,
			dbmodels.TenantChargingSettingColumns.GasPrice,
			dbmodels.TenantChargingSettingColumns.GasMPGEquivalent,
			dbmodels.TenantChargingSettingColumns.VehicleKWHPerMile,
			dbmodels.TenantChargingSettingColumns.Currency,
			dbmodels.TenantChargingSettingColumns.UpdatedAt,
		),
		boil.Infer(),
	)
}
