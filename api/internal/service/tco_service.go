package service

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
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

// costEligibleCETypeList is CostEligibleCETypes as the slice fetch-api's
// `types` filter wants. Sorted so the generated query is stable.
func costEligibleCETypeList() []string {
	types := make([]string, 0, len(CostEligibleCETypes))
	for t := range CostEligibleCETypes {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
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
	// ID is the document's parsed CE id — the reference a cost-amendment CE
	// (see AttestCostAmendment) or a future edit needs to target it.
	ID             string  `json:"id"`
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
	attestSvc    AttestService
}

func NewTCOService(logger *zerolog.Logger, pdb *db.Store, fetchAPI *gateway.FetchAPI, authProvider *gateway.DimoAuthProvider, vehicleSvc *VehicleService, attestSvc AttestService) *TCOService {
	return &TCOService{
		logger:       logger,
		pdb:          pdb,
		fetchAPI:     fetchAPI,
		authProvider: authProvider,
		vehicleSvc:   vehicleSvc,
		attestSvc:    attestSvc,
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
		if f, ok := row.PurchasePrice.Float64(); ok {
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

// VehicleTCOSummary is one vehicle's cost breakdown for the TCO report.
type VehicleTCOSummary struct {
	VehicleTokenID     int64              `json:"tokenId"`
	VehicleLabel       string             `json:"vehicleLabel"`
	VIN                string             `json:"vin,omitempty"`
	OperatingCost      float64            `json:"operatingCost"`
	CostByCategory     map[string]float64 `json:"costByCategory"`
	AcquisitionCost    float64            `json:"acquisitionCost"`
	DepreciationToDate float64            `json:"depreciationToDate"`
	TotalTCO           float64            `json:"totalTco"`
	Settings           TCOSettings        `json:"settings"`
	LineItems          []LineItem         `json:"lineItems"`
	// MissingAmounts are cost-eligible, non-tombstoned documents that have no
	// amount — neither inline (extractAmount) nor via a cost-amendment CE
	// (AttestCostAmendment). Amount/Currency are zero-valued; the frontend
	// offers a way to backfill one via PUT /tco/vehicle/:tokenId/backfill/:documentId.
	MissingAmounts []LineItem `json:"missingAmounts,omitempty"`
	// PermissionsRequired mirrors DocumentsController.ListDocuments: the dev
	// license lacks SACD permissions on this vehicle, so its document list
	// (and therefore its cost figures) couldn't be read. Acquisition/
	// depreciation figures are still populated from vehicle_tco_settings,
	// which doesn't depend on fetch-api access.
	PermissionsRequired bool `json:"permissionsRequired,omitempty"`
}

// FleetTotals sums each VehicleTCOSummary field across the fleet.
type FleetTotals struct {
	OperatingCost      float64 `json:"operatingCost"`
	AcquisitionCost    float64 `json:"acquisitionCost"`
	DepreciationToDate float64 `json:"depreciationToDate"`
	TotalTCO           float64 `json:"totalTco"`
}

// FleetTCOSummary is the fleet-wide rollup: each vehicle's summary plus totals.
type FleetTCOSummary struct {
	Vehicles []VehicleTCOSummary `json:"vehicles"`
	Fleet    FleetTotals         `json:"fleet"`
}

// vehicleLabel formats "<year> <make> <model>", falling back to "Vehicle #<id>".
func vehicleLabel(v models.Vehicle) string {
	d := v.Definition
	parts := make([]string, 0, 3)
	if d.Year != 0 {
		parts = append(parts, strconv.Itoa(d.Year))
	}
	if d.Make != "" {
		parts = append(parts, d.Make)
	}
	if d.Model != "" {
		parts = append(parts, d.Model)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Vehicle #%d", v.TokenID)
	}
	return strings.Join(parts, " ")
}

// descriptionFor derives a human label for a line item from its parsed data's
// "fileName"/"name" field, falling back to the CE type's last segment.
func descriptionFor(e gateway.AttestationEntry) string {
	var payload struct {
		Name     string `json:"name"`
		FileName string `json:"fileName"`
	}
	if len(e.Data) > 0 {
		_ = json.Unmarshal(e.Data, &payload)
	}
	if payload.FileName != "" {
		return payload.FileName
	}
	if payload.Name != "" {
		return payload.Name
	}
	parts := strings.Split(e.Type, ".")
	return parts[len(parts)-1]
}

type costAmendment struct {
	amount   float64
	currency string
}

// costAmendments scans entries for dimo.document.vehicle.cost-amendment CEs
// (see AttestCostAmendment) and returns the amount/currency each names,
// keyed by the documentId it overlays. Tombstoned amendments are excluded so
// a mistaken backfill can be un-done the same way a document delete is.
func costAmendments(entries []gateway.AttestationEntry, tombstoned map[string]struct{}) map[string]costAmendment {
	out := map[string]costAmendment{}
	for _, e := range entries {
		if e.Type != CostAmendmentCloudEventType {
			continue
		}
		if _, gone := tombstoned[e.ID]; gone {
			continue
		}
		var payload struct {
			DocumentID string  `json:"documentId"`
			Amount     float64 `json:"amount"`
			Currency   string  `json:"currency"`
		}
		if err := json.Unmarshal(e.Data, &payload); err != nil || payload.DocumentID == "" {
			continue
		}
		currency := payload.Currency
		if currency == "" {
			currency = "USD"
		}
		out[payload.DocumentID] = costAmendment{amount: payload.Amount, currency: currency}
	}
	return out
}

// VehicleSummary builds one vehicle's TCO breakdown: cost-eligible document
// amounts summed by category, plus acquisition/depreciation if settings exist.
// allowedGroupIDs scopes the lookup to a limited member's accessible groups
// (nil for owners/full-access members); see VehicleService.GetVehicle.
func (s *TCOService) VehicleSummary(ctx context.Context, tenant models.Tenant, tokenID int64, allowedGroupIDs []string) (*VehicleTCOSummary, error) {
	vehicle, err := s.vehicleSvc.GetVehicle(ctx, tenant, tokenID, allowedGroupIDs)
	if err != nil {
		return nil, fmt.Errorf("get vehicle: %w", err)
	}
	label := vehicleLabel(*vehicle)

	tokenDID := s.authProvider.BuildVehicleDID(uint64(tokenID))
	// Enumerated, server-side-filtered types. Reading unfiltered would take the
	// most recent 500 CEs of every kind — telemetry included — and silently
	// drop the documents the costs are computed from.
	entries, err := s.fetchAPI.ListByDIDAndTypes(tenant, tokenDID, gateway.TCOCETypes(costEligibleCETypeList()), 500)
	permissionsRequired := false
	if err != nil {
		// Mirrors DocumentsController.ListDocuments: a 403 here means the dev
		// license lacks SACD permissions on this vehicle, not that something
		// is actually broken. Degrade to an empty (but still valid) summary —
		// acquisition/depreciation below still works since it's DB-only —
		// rather than failing the whole vehicle out of the fleet report.
		if strings.Contains(err.Error(), "lacks permissions") || strings.Contains(err.Error(), "status code 403") {
			permissionsRequired = true
		} else {
			return nil, fmt.Errorf("list documents: %w", err)
		}
	}

	tombstoned := gateway.TombstonedIDs(entries)
	amendments := costAmendments(entries, tombstoned)

	lineItems := make([]LineItem, 0, len(entries))
	missingAmounts := make([]LineItem, 0)
	for _, e := range entries {
		if _, gone := tombstoned[e.ID]; gone {
			continue
		}
		if !isCostEligible(e.Type) {
			continue
		}
		li := LineItem{
			ID:             e.ID,
			VehicleTokenID: tokenID,
			VehicleLabel:   label,
			VIN:            vehicle.VIN,
			Date:           e.Time,
			Category:       e.Type,
			Description:    descriptionFor(e),
		}
		if amount, currency, ok := extractAmount(e.Data); ok {
			li.Amount, li.Currency = amount, currency
			lineItems = append(lineItems, li)
		} else if am, ok := amendments[e.ID]; ok {
			li.Amount, li.Currency = am.amount, am.currency
			lineItems = append(lineItems, li)
		} else {
			missingAmounts = append(missingAmounts, li)
		}
	}

	settings, err := s.GetSettings(ctx, tenant.ID, tokenID)
	if err != nil {
		return nil, fmt.Errorf("get tco settings: %w", err)
	}

	summary := &VehicleTCOSummary{
		VehicleTokenID:      tokenID,
		VehicleLabel:        label,
		VIN:                 vehicle.VIN,
		CostByCategory:      sumLineItemsByCategory(lineItems),
		Settings:            settings,
		LineItems:           lineItems,
		MissingAmounts:      missingAmounts,
		PermissionsRequired: permissionsRequired,
	}
	for _, v := range summary.CostByCategory {
		summary.OperatingCost += v
	}
	if settings.PurchasePrice != nil {
		summary.AcquisitionCost = *settings.PurchasePrice
		if settings.PurchaseDate != nil && settings.UsefulLifeYears != nil {
			purchaseDate, perr := time.Parse("2006-01-02", *settings.PurchaseDate)
			if perr == nil {
				summary.DepreciationToDate = straightLineDepreciation(*settings.PurchasePrice, purchaseDate, *settings.UsefulLifeYears, time.Now())
			}
		}
	}
	summary.TotalTCO = summary.OperatingCost + summary.AcquisitionCost
	return summary, nil
}

// BackfillAmount attaches a dollar amount to a document that was uploaded
// without one (see MissingAmounts), by publishing a cost-amendment CE that
// references it — the original document CE is immutable and never touched.
// Returns the new amendment CE's id.
func (s *TCOService) BackfillAmount(tenant models.Tenant, tokenID int64, documentID string, amount float64, currency string) (string, error) {
	if documentID == "" {
		return "", fmt.Errorf("documentID is required")
	}
	return s.attestSvc.AttestCostAmendment(tenant, uint64(tokenID), documentID, amount, currency)
}

// FleetSummary builds the TCO rollup for every vehicle in the tenant. Vehicles
// are processed sequentially — one fetch-api round trip each — which is
// acceptable for the fleet sizes this app targets; revisit with a worker pool
// if that stops being true. A single vehicle's failure is logged and skipped
// rather than failing the whole report.
// allowedGroupIDs scopes the vehicle list to a limited member's accessible
// groups (nil for owners/full-access members); see VehicleService.ListVehicles.
func (s *TCOService) FleetSummary(ctx context.Context, tenant models.Tenant, allowedGroupIDs []string) (*FleetTCOSummary, error) {
	vehicles, err := s.vehicleSvc.ListVehicles(ctx, tenant, allowedGroupIDs)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	out := &FleetTCOSummary{Vehicles: make([]VehicleTCOSummary, 0, len(vehicles))}
	for _, v := range vehicles {
		summary, err := s.VehicleSummary(ctx, tenant, v.TokenID, allowedGroupIDs)
		if err != nil {
			s.logger.Warn().Err(err).Int64("tokenID", v.TokenID).Msg("tco vehicle summary failed, skipping")
			continue
		}
		out.Vehicles = append(out.Vehicles, *summary)
		out.Fleet.OperatingCost += summary.OperatingCost
		out.Fleet.AcquisitionCost += summary.AcquisitionCost
		out.Fleet.DepreciationToDate += summary.DepreciationToDate
		out.Fleet.TotalTCO += summary.TotalTCO
	}
	return out, nil
}

// ceTypeToLabel mirrors web/src/utils/document-categories.ts's CE_TYPE_TO_LABEL
// so the CSV export reads the same as the app. Duplicated deliberately: the
// frontend map is TypeScript and can't be imported into Go.
var ceTypeToLabel = map[string]string{
	"dimo.document.vehicle.service.invoice":  "Service & parts",
	"dimo.document.vehicle.insurance":        "Insurance",
	"dimo.document.vehicle.registration":     "Registration",
	"dimo.document.vehicle.inspection":       "Inspection",
	"dimo.document.vehicle.finance":          "Finance",
	"dimo.document.vehicle.regulatory.other": "Regulatory",
	"dimo.document.vehicle.maintenance":      "Service & parts",
	"dimo.document.vehicle.expense":          "Other",
	FuelCloudEventType:                       "Fuel",
}

func categoryLabelForCSV(ceType string) string {
	if l, ok := ceTypeToLabel[ceType]; ok {
		return l
	}
	return ceType
}

// BuildCSV renders line items (plus trailing acquisition/depreciation summary
// rows per vehicle) as CSV text.
func BuildCSV(summaries []VehicleTCOSummary) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"vehicle", "vin", "date", "category", "description", "amount", "currency"})
	for _, v := range summaries {
		for _, li := range v.LineItems {
			_ = w.Write([]string{
				li.VehicleLabel, li.VIN, li.Date, categoryLabelForCSV(li.Category), li.Description,
				strconv.FormatFloat(li.Amount, 'f', 2, 64), li.Currency,
			})
		}
		if v.Settings.PurchasePrice != nil {
			_ = w.Write([]string{
				v.VehicleLabel, v.VIN, "(acquisition)", "Acquisition", "Purchase price",
				strconv.FormatFloat(*v.Settings.PurchasePrice, 'f', 2, 64), v.Settings.Currency,
			})
			_ = w.Write([]string{
				v.VehicleLabel, v.VIN, "(acquisition)", "Depreciation", "Depreciation to date",
				strconv.FormatFloat(-v.DepreciationToDate, 'f', 2, 64), v.Settings.Currency,
			})
		}
	}
	w.Flush()
	return b.String()
}
