package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/types"
	"github.com/ericlagergren/decimal"
	"github.com/rs/zerolog"
)

// FuelCloudEventType is the CE type for fuel receipts — broken out from the
// generic "expense" bucket since fuel is usually a fleet's largest recurring
// cost. See docs/superpowers/specs/2026-07-06-tco-reporting-design.md.
const FuelCloudEventType = "dimo.document.vehicle.fuel"

// CostEligibleCETypes are the canonical CE types whose amounts count toward
// TCO operating costs. Title/Note/Condition are informational, not spend.
var CostEligibleCETypes = map[string]bool{
	"dimo.document.vehicle.service.invoice":  true,
	"dimo.document.vehicle.insurance":        true,
	"dimo.document.vehicle.registration":     true,
	"dimo.document.vehicle.inspection":       true,
	"dimo.document.vehicle.finance":          true,
	"dimo.document.vehicle.regulatory.other": true,
	"dimo.document.vehicle.maintenance":      true,
	"dimo.document.vehicle.expense":          true,
	FuelCloudEventType:                       true,
}

// isCostEligible reports whether a document's CE type counts toward TCO
// operating costs.
func isCostEligible(ceType string) bool {
	return CostEligibleCETypes[ceType]
}

// extractAmount pulls "amount"/"currency" out of a parsed document CE's data
// payload. ok is false when data is empty, unparseable, or has no numeric
// amount field — callers should skip the document rather than count it as $0.
func extractAmount(data json.RawMessage) (amount float64, currency string, ok bool) {
	if len(data) == 0 {
		return 0, "", false
	}
	var payload struct {
		Amount   *float64 `json:"amount"`
		Currency string   `json:"currency"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.Amount == nil {
		return 0, "", false
	}
	currency = payload.Currency
	if currency == "" {
		currency = "USD"
	}
	return *payload.Amount, currency, true
}

// straightLineDepreciation returns the depreciation accrued from purchaseDate
// through asOf, straight-line over usefulLifeYears, capped at purchasePrice.
// Returns 0 if usefulLifeYears <= 0, purchasePrice <= 0, or asOf precedes
// purchaseDate.
func straightLineDepreciation(purchasePrice float64, purchaseDate time.Time, usefulLifeYears int, asOf time.Time) float64 {
	if usefulLifeYears <= 0 || purchasePrice <= 0 {
		return 0
	}
	elapsedYears := asOf.Sub(purchaseDate).Hours() / 24 / 365.25
	if elapsedYears <= 0 {
		return 0
	}
	dep := (purchasePrice / float64(usefulLifeYears)) * elapsedYears
	if dep > purchasePrice {
		return purchasePrice
	}
	return dep
}

// LineItem is one cost-eligible document, flattened for reporting/export.
type LineItem struct {
	VehicleTokenID int64   `json:"vehicleTokenId"`
	VehicleLabel   string  `json:"vehicleLabel"`
	VIN            string  `json:"vin"`
	Date           string  `json:"date"`
	Category       string  `json:"category"`
	Description    string  `json:"description"`
	Amount         float64 `json:"amount"`
	Currency       string  `json:"currency"`
}

// sumLineItemsByCategory totals LineItem.Amount grouped by CE type.
func sumLineItemsByCategory(items []LineItem) map[string]float64 {
	out := map[string]float64{}
	for _, li := range items {
		out[li.Category] += li.Amount
	}
	return out
}

// TCOSettings is a vehicle's optional acquisition/depreciation inputs.
// Nil pointer fields mean "not set" — the vehicle shows operating costs only.
type TCOSettings struct {
	VehicleTokenID  int64    `json:"tokenId"`
	PurchasePrice   *float64 `json:"purchasePrice,omitempty"`
	PurchaseDate    *string  `json:"purchaseDate,omitempty"` // YYYY-MM-DD
	UsefulLifeYears *int     `json:"usefulLifeYears,omitempty"`
	Currency        string   `json:"currency"`
}

// TCOService builds cost-of-ownership reports from Glovebox documents plus
// each vehicle's optional acquisition/depreciation settings.
type TCOService struct {
	logger       *zerolog.Logger
	pdb          *db.Store
	fetchAPI     *gateway.FetchAPI
	authProvider *gateway.DimoAuthProvider
	vehicleSvc   *VehicleService
}

func NewTCOService(logger *zerolog.Logger, pdb *db.Store, fetchAPI *gateway.FetchAPI, authProvider *gateway.DimoAuthProvider, vehicleSvc *VehicleService) *TCOService {
	return &TCOService{
		logger:       logger,
		pdb:          pdb,
		fetchAPI:     fetchAPI,
		authProvider: authProvider,
		vehicleSvc:   vehicleSvc,
	}
}

// GetSettings returns a vehicle's TCO settings, or a zero-value TCOSettings
// (Currency defaulted to USD, other fields nil) if none have been saved.
func (s *TCOService) GetSettings(ctx context.Context, tenantID string, tokenID int64) (TCOSettings, error) {
	row, err := dbmodels.FindVehicleTcoSetting(ctx, s.pdb.DBS().Reader, tenantID, tokenID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TCOSettings{VehicleTokenID: tokenID, Currency: "USD"}, nil
		}
		return TCOSettings{}, fmt.Errorf("get tco settings: %w", err)
	}
	out := TCOSettings{VehicleTokenID: tokenID, Currency: row.Currency}
	if row.PurchasePrice.Big != nil {
		if f, ok := row.PurchasePrice.Big.Float64(); ok {
			out.PurchasePrice = &f
		}
	}
	if row.PurchaseDate.Valid {
		d := row.PurchaseDate.Time.Format("2006-01-02")
		out.PurchaseDate = &d
	}
	if row.UsefulLifeYears.Valid {
		v := row.UsefulLifeYears.Int
		out.UsefulLifeYears = &v
	}
	return out, nil
}

// UpsertSettings full-replaces a vehicle's TCO settings. An empty in.Currency
// defaults to "USD".
func (s *TCOService) UpsertSettings(ctx context.Context, tenantID string, tokenID int64, in TCOSettings) error {
	currency := in.Currency
	if currency == "" {
		currency = "USD"
	}
	row := &dbmodels.VehicleTcoSetting{
		TenantID: tenantID,
		TokenID:  tokenID,
		Currency: currency,
	}
	if in.PurchasePrice != nil {
		row.PurchasePrice = types.NewNullDecimal(new(decimal.Big).SetFloat64(*in.PurchasePrice))
	}
	if in.PurchaseDate != nil {
		t, err := time.Parse("2006-01-02", *in.PurchaseDate)
		if err != nil {
			return fmt.Errorf("invalid purchaseDate %q: %w", *in.PurchaseDate, err)
		}
		row.PurchaseDate = null.TimeFrom(t)
	}
	if in.UsefulLifeYears != nil {
		row.UsefulLifeYears = null.IntFrom(*in.UsefulLifeYears)
	}
	now := time.Now()
	row.CreatedAt = now
	row.UpdatedAt = now
	return row.Upsert(ctx, s.pdb.DBS().Writer, true,
		[]string{dbmodels.VehicleTcoSettingColumns.TenantID, dbmodels.VehicleTcoSettingColumns.TokenID},
		boil.Whitelist(
			dbmodels.VehicleTcoSettingColumns.PurchasePrice,
			dbmodels.VehicleTcoSettingColumns.PurchaseDate,
			dbmodels.VehicleTcoSettingColumns.UsefulLifeYears,
			dbmodels.VehicleTcoSettingColumns.Currency,
			dbmodels.VehicleTcoSettingColumns.UpdatedAt,
		),
		boil.Infer(),
	)
}
