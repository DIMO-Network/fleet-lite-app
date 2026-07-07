package service

import (
	"encoding/json"
	"time"
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
