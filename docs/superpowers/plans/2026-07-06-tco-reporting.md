# TCO Reporting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a fleet-wide "TCO" (total cost of ownership) tab that turns Glovebox documents into a cost analysis per vehicle and across the fleet, including straight-line depreciation, with CSV export.

**Architecture:** A new Go service (`TCOService`) sums dollar amounts already carried in Glovebox documents' `parsedData` (fetched via the existing `fetchAPI.ListByDID`), combines them with an optional per-vehicle acquisition/depreciation settings row (new Postgres table), and exposes the result through a new controller. The frontend adds an "Amount" field to the existing upload-confirm modal, a new "Fuel" document category, and a new `tco-view.ts` with a fleet table and in-page vehicle drilldown.

**Tech Stack:** Go + Fiber + PostgreSQL + SQLBoiler (backend), Lit + TypeScript + Vite (frontend). Follows this repo's existing Glovebox/document pipeline exactly — no new external integrations.

## Global Constraints

- Straight-line depreciation only: `(purchasePrice / usefulLifeYears) × yearsElapsed`, capped at `purchasePrice`. No declining-balance or market-value methods.
- Cost-eligible CE types (count toward operating cost): `dimo.document.vehicle.service.invoice`, `dimo.document.vehicle.insurance`, `dimo.document.vehicle.registration`, `dimo.document.vehicle.inspection`, `dimo.document.vehicle.finance`, `dimo.document.vehicle.regulatory.other`, `dimo.document.vehicle.maintenance`, `dimo.document.vehicle.expense`, `dimo.document.vehicle.fuel`. Excluded: `dimo.document.vehicle.title`, `dimo.document.vehicle.note`, `dimo.document.vehicle.condition`.
- Amounts are captured via manual entry at the upload-confirm step only — no post-upload edit, no trusting extraction alone.
- All-time totals only — no date-range filtering in v1.
- CSV export is line-item (one row per document), with trailing acquisition/depreciation summary rows per vehicle.
- No new document storage system — documents remain DIS CloudEvents via the existing extract→confirm→attest pipeline. Only new persisted state is the optional `vehicle_tco_settings` table (acquisition price/date/useful-life per vehicle).
- Follow existing repo conventions exactly: SQLBoiler-generated models (never hand-edit `internal/db/models/*.go`), `make migrate` + `make sqlboiler` for schema changes, per-controller `vehicleInTenant` helper (duplicated per controller, matching `documents.go`/`telemetry.go`), Lit + `@lit/localize` `msg()` for all user-facing strings.

---

### Task 1: `vehicle_tco_settings` migration + SQLBoiler model

**Files:**
- Create: `api/internal/db/migrations/20260710120000_vehicle_tco_settings.sql`
- Generated (do not hand-edit): `api/internal/db/models/vehicle_tco_settings.go`

**Interfaces:**
- Produces: table `vehicle_tco_settings` with SQLBoiler-generated Go type `dbmodels.VehicleTCOSetting` (fields `TenantID string`, `TokenID int64`, `PurchasePrice null.Float64`, `PurchaseDate null.Time`, `UsefulLifeYears null.Int`, `Currency string`, `CreatedAt time.Time`, `UpdatedAt time.Time`), `dbmodels.VehicleTCOSettingColumns` (column-name constants), `dbmodels.FindVehicleTCOSetting(ctx, exec, tenantID string, tokenID int64, selectCols ...string) (*VehicleTCOSetting, error)`. Task 3 consumes these.

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Optional per-vehicle acquisition/depreciation inputs for the TCO report.
-- Scoped by tenant like vehicle_favorites. All purchase fields are nullable —
-- a vehicle with no row (or nulls) just shows operating costs, no
-- acquisition/depreciation line. See
-- docs/superpowers/specs/2026-07-06-tco-reporting-design.md.
CREATE TABLE IF NOT EXISTS vehicle_tco_settings (
    tenant_id         UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    token_id          BIGINT NOT NULL,
    purchase_price    NUMERIC,
    purchase_date     DATE,
    useful_life_years INTEGER,
    currency          TEXT NOT NULL DEFAULT 'USD',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id)
);

CREATE INDEX IF NOT EXISTS idx_vehicle_tco_settings_tenant_id ON vehicle_tco_settings (tenant_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP TABLE IF EXISTS vehicle_tco_settings;

-- +goose StatementEnd
```

- [ ] **Step 2: Run the migration against your local dev DB**

Run: `cd api && make migrate`
Expected: output includes `OK   20260710120000_vehicle_tco_settings.sql` and no errors.

- [ ] **Step 3: Regenerate SQLBoiler models**

Run: `cd api && make sqlboiler`
Expected: `internal/db/models/vehicle_tco_settings.go` is created (generated — do not hand-edit). No errors.

- [ ] **Step 4: Verify the build still compiles**

Run: `cd api && go build ./...`
Expected: exits 0.

- [ ] **Step 5: Commit**

```bash
git add api/internal/db/migrations/20260710120000_vehicle_tco_settings.sql api/internal/db/models/vehicle_tco_settings.go
git commit -m "feat(api): add vehicle_tco_settings table for TCO acquisition/depreciation inputs"
```

---

### Task 2: TCO pure calculation functions + unit tests

**Files:**
- Create: `api/internal/service/tco_service.go`
- Create: `api/internal/service/tco_service_test.go`

**Interfaces:**
- Consumes: nothing (pure functions, no DB/network).
- Produces: `const FuelCloudEventType string`, `var CostEligibleCETypes map[string]bool`, `func isCostEligible(ceType string) bool`, `func extractAmount(data json.RawMessage) (amount float64, currency string, ok bool)`, `func straightLineDepreciation(purchasePrice float64, purchaseDate time.Time, usefulLifeYears int, asOf time.Time) float64`, `type LineItem struct{ VehicleTokenID int64; VehicleLabel string; VIN string; Date string; Category string; Description string; Amount float64; Currency string }`, `func sumLineItemsByCategory(items []LineItem) map[string]float64`. Tasks 3–5 consume all of these.

- [ ] **Step 1: Write the failing tests**

```go
// api/internal/service/tco_service_test.go
package service

import (
	"encoding/json"
	"testing"
	"time"
)

func TestIsCostEligible(t *testing.T) {
	cases := []struct {
		ceType string
		want   bool
	}{
		{"dimo.document.vehicle.service.invoice", true},
		{"dimo.document.vehicle.insurance", true},
		{"dimo.document.vehicle.fuel", true},
		{"dimo.document.vehicle.title", false},
		{"dimo.document.vehicle.note", false},
		{"dimo.document.vehicle.condition", false},
		{"dimo.document.unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.ceType, func(t *testing.T) {
			if got := isCostEligible(tc.ceType); got != tc.want {
				t.Fatalf("isCostEligible(%q) = %v, want %v", tc.ceType, got, tc.want)
			}
		})
	}
}

func TestExtractAmount(t *testing.T) {
	cases := []struct {
		name         string
		data         string
		wantAmount   float64
		wantCurrency string
		wantOK       bool
	}{
		{"amount and currency", `{"amount": 412.5, "currency": "EUR"}`, 412.5, "EUR", true},
		{"amount defaults currency to USD", `{"amount": 10}`, 10, "USD", true},
		{"no amount field", `{"vin": "1HGCM82633A123456"}`, 0, "", false},
		{"empty data", ``, 0, "", false},
		{"malformed json", `not json`, 0, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			amount, currency, ok := extractAmount(json.RawMessage(tc.data))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if amount != tc.wantAmount || currency != tc.wantCurrency {
				t.Fatalf("got (%v, %q), want (%v, %q)", amount, currency, tc.wantAmount, tc.wantCurrency)
			}
		})
	}
}

func TestStraightLineDepreciation(t *testing.T) {
	purchase := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name            string
		purchasePrice   float64
		usefulLifeYears int
		asOf            time.Time
		want            float64
	}{
		{"half life elapsed", 20000, 10, purchase.AddDate(5, 0, 0), 10000},
		{"before purchase", 20000, 10, purchase.AddDate(-1, 0, 0), 0},
		{"fully depreciated, capped", 20000, 10, purchase.AddDate(50, 0, 0), 20000},
		{"zero useful life", 20000, 0, purchase.AddDate(5, 0, 0), 0},
		{"zero price", 0, 10, purchase.AddDate(5, 0, 0), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := straightLineDepreciation(tc.purchasePrice, purchase, tc.usefulLifeYears, tc.asOf)
			if diff := got - tc.want; diff > 1 || diff < -1 {
				t.Fatalf("straightLineDepreciation(...) = %v, want ~%v", got, tc.want)
			}
		})
	}
}

func TestSumLineItemsByCategory(t *testing.T) {
	items := []LineItem{
		{Category: "dimo.document.vehicle.insurance", Amount: 100},
		{Category: "dimo.document.vehicle.insurance", Amount: 50},
		{Category: "dimo.document.vehicle.fuel", Amount: 40},
	}
	got := sumLineItemsByCategory(items)
	if got["dimo.document.vehicle.insurance"] != 150 {
		t.Fatalf("insurance total = %v, want 150", got["dimo.document.vehicle.insurance"])
	}
	if got["dimo.document.vehicle.fuel"] != 40 {
		t.Fatalf("fuel total = %v, want 40", got["dimo.document.vehicle.fuel"])
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail (package doesn't exist yet)**

Run: `cd api && go test ./internal/service/... -run 'TestIsCostEligible|TestExtractAmount|TestStraightLineDepreciation|TestSumLineItemsByCategory' -v`
Expected: FAIL — compile error, undefined `isCostEligible` etc.

- [ ] **Step 3: Write the implementation**

```go
// api/internal/service/tco_service.go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go test ./internal/service/... -run 'TestIsCostEligible|TestExtractAmount|TestStraightLineDepreciation|TestSumLineItemsByCategory' -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add api/internal/service/tco_service.go api/internal/service/tco_service_test.go
git commit -m "feat(api): add TCO cost-eligibility, amount extraction, and depreciation math"
```

---

### Task 3: TCO settings CRUD (DB-backed)

**Files:**
- Modify: `api/internal/service/tco_service.go` (append)

**Interfaces:**
- Consumes: `dbmodels.VehicleTCOSetting`, `dbmodels.VehicleTCOSettingColumns`, `dbmodels.FindVehicleTCOSetting` (Task 1); `db.Store` from `github.com/DIMO-Network/shared/pkg/db` (existing, see `internal/service/user_prefs.go`).
- Produces: `type TCOSettings struct{ VehicleTokenID int64; PurchasePrice *float64; PurchaseDate *string; UsefulLifeYears *int; Currency string }`, `type TCOService struct{...}`, `func NewTCOService(logger *zerolog.Logger, pdb *db.Store, fetchAPI *gateway.FetchAPI, authProvider *gateway.DimoAuthProvider, vehicleSvc *VehicleService) *TCOService`, `func (s *TCOService) GetSettings(ctx, tenantID string, tokenID int64) (TCOSettings, error)`, `func (s *TCOService) UpsertSettings(ctx, tenantID string, tokenID int64, in TCOSettings) error`. Tasks 4–5 consume all of these.

- [ ] **Step 1: Append the settings types + service scaffold + CRUD methods**

```go
// Append to api/internal/service/tco_service.go

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
	"github.com/rs/zerolog"
)

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
	row, err := dbmodels.FindVehicleTCOSetting(ctx, s.pdb.DBS().Reader, tenantID, tokenID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TCOSettings{VehicleTokenID: tokenID, Currency: "USD"}, nil
		}
		return TCOSettings{}, fmt.Errorf("get tco settings: %w", err)
	}
	out := TCOSettings{VehicleTokenID: tokenID, Currency: row.Currency}
	if row.PurchasePrice.Valid {
		v := row.PurchasePrice.Float64
		out.PurchasePrice = &v
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
	row := &dbmodels.VehicleTCOSetting{
		TenantID: tenantID,
		TokenID:  tokenID,
		Currency: currency,
	}
	if in.PurchasePrice != nil {
		row.PurchasePrice = null.Float64From(*in.PurchasePrice)
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
		[]string{dbmodels.VehicleTCOSettingColumns.TenantID, dbmodels.VehicleTCOSettingColumns.TokenID},
		boil.Whitelist(
			dbmodels.VehicleTCOSettingColumns.PurchasePrice,
			dbmodels.VehicleTCOSettingColumns.PurchaseDate,
			dbmodels.VehicleTCOSettingColumns.UsefulLifeYears,
			dbmodels.VehicleTCOSettingColumns.Currency,
			dbmodels.VehicleTCOSettingColumns.UpdatedAt,
		),
		boil.Infer(),
	)
}
```

Note: `encoding/json` is already imported from Task 2 — merge the import blocks into one `import (...)` group at the top of the file rather than two separate blocks.

- [ ] **Step 2: Verify it builds**

Run: `cd api && go build ./...`
Expected: exits 0. (No new tests here — this is thin DB CRUD following the exact pattern of `internal/service/user_prefs.go`, which has no unit tests either since it requires a live DB.)

- [ ] **Step 3: Commit**

```bash
git add api/internal/service/tco_service.go
git commit -m "feat(api): add TCO settings get/upsert backed by vehicle_tco_settings"
```

---

### Task 4: Vehicle + fleet TCO summary assembly and CSV export

**Files:**
- Modify: `api/internal/service/tco_service.go` (append)

**Interfaces:**
- Consumes: `s.vehicleSvc.GetVehicle(ctx, tenantID string, tokenID int64) (*models.Vehicle, error)`, `s.vehicleSvc.ListVehicles(ctx, tenantID string) ([]models.Vehicle, error)` (existing, `internal/service/vehicle.go`); `s.fetchAPI.ListByDID(tenant models.Tenant, tokenDID string, limit int) ([]gateway.AttestationEntry, error)` (existing, `internal/gateway/fetch_api.go`); `s.authProvider.BuildVehicleDID(tokenID uint64) string` (existing); `TCOSettings`, `LineItem`, `isCostEligible`, `extractAmount`, `straightLineDepreciation`, `sumLineItemsByCategory` (Tasks 2–3).
- Produces: `type VehicleTCOSummary struct{...}`, `type FleetTotals struct{...}`, `type FleetTCOSummary struct{...}`, `func (s *TCOService) VehicleSummary(ctx, tenant models.Tenant, tokenID int64) (*VehicleTCOSummary, error)`, `func (s *TCOService) FleetSummary(ctx, tenant models.Tenant) (*FleetTCOSummary, error)`, `func BuildCSV(summaries []VehicleTCOSummary) string`. Task 5 consumes all of these.

- [ ] **Step 1: Append the summary types + assembly + CSV methods**

```go
// Append to api/internal/service/tco_service.go — add these to the existing
// import block: "encoding/csv", "strconv", "strings",
// "github.com/DIMO-Network/fleet-lite-app/internal/models"

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

// VehicleSummary builds one vehicle's TCO breakdown: cost-eligible document
// amounts summed by category, plus acquisition/depreciation if settings exist.
func (s *TCOService) VehicleSummary(ctx context.Context, tenant models.Tenant, tokenID int64) (*VehicleTCOSummary, error) {
	vehicle, err := s.vehicleSvc.GetVehicle(ctx, tenant.ID, tokenID)
	if err != nil {
		return nil, fmt.Errorf("get vehicle: %w", err)
	}
	label := vehicleLabel(*vehicle)

	tokenDID := s.authProvider.BuildVehicleDID(uint64(tokenID))
	entries, err := s.fetchAPI.ListByDID(tenant, tokenDID, 500)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}

	lineItems := make([]LineItem, 0, len(entries))
	for _, e := range entries {
		if !isCostEligible(e.Type) {
			continue
		}
		amount, currency, ok := extractAmount(e.Data)
		if !ok {
			continue
		}
		lineItems = append(lineItems, LineItem{
			VehicleTokenID: tokenID,
			VehicleLabel:   label,
			VIN:            vehicle.VIN,
			Date:           e.Time,
			Category:       e.Type,
			Description:    descriptionFor(e),
			Amount:         amount,
			Currency:       currency,
		})
	}

	settings, err := s.GetSettings(ctx, tenant.ID, tokenID)
	if err != nil {
		return nil, fmt.Errorf("get tco settings: %w", err)
	}

	summary := &VehicleTCOSummary{
		VehicleTokenID: tokenID,
		VehicleLabel:   label,
		VIN:            vehicle.VIN,
		CostByCategory: sumLineItemsByCategory(lineItems),
		Settings:       settings,
		LineItems:      lineItems,
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

// FleetSummary builds the TCO rollup for every vehicle in the tenant. Vehicles
// are processed sequentially — one fetch-api round trip each — which is
// acceptable for the fleet sizes this app targets; revisit with a worker pool
// if that stops being true. A single vehicle's failure is logged and skipped
// rather than failing the whole report.
func (s *TCOService) FleetSummary(ctx context.Context, tenant models.Tenant) (*FleetTCOSummary, error) {
	vehicles, err := s.vehicleSvc.ListVehicles(ctx, tenant.ID)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	out := &FleetTCOSummary{Vehicles: make([]VehicleTCOSummary, 0, len(vehicles))}
	for _, v := range vehicles {
		summary, err := s.VehicleSummary(ctx, tenant, v.TokenID)
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
```

- [ ] **Step 2: Verify it builds**

Run: `cd api && go build ./...`
Expected: exits 0.

- [ ] **Step 3: Add a unit test for the CSV builder (pure function, no DB/network)**

```go
// Append to api/internal/service/tco_service_test.go
func TestBuildCSV(t *testing.T) {
	price := 20000.0
	summaries := []VehicleTCOSummary{
		{
			VehicleLabel:       "2021 Subaru Ascent",
			VIN:                "1HGCM82633A123456",
			DepreciationToDate: 5000,
			Settings:           TCOSettings{PurchasePrice: &price, Currency: "USD"},
			LineItems: []LineItem{
				{VehicleLabel: "2021 Subaru Ascent", VIN: "1HGCM82633A123456", Date: "2026-03-14", Category: "dimo.document.vehicle.service.invoice", Description: "invoice.pdf", Amount: 412.5, Currency: "USD"},
			},
		},
	}
	got := BuildCSV(summaries)
	wantHeader := "vehicle,vin,date,category,description,amount,currency"
	if !strings.Contains(got, wantHeader) {
		t.Fatalf("missing header row, got:\n%s", got)
	}
	if !strings.Contains(got, "412.50,USD") {
		t.Fatalf("missing line item row, got:\n%s", got)
	}
	if !strings.Contains(got, "20000.00,USD") {
		t.Fatalf("missing acquisition row, got:\n%s", got)
	}
	if !strings.Contains(got, "-5000.00,USD") {
		t.Fatalf("missing depreciation row, got:\n%s", got)
	}
}
```

Add `"strings"` to the test file's import block if not already present.

- [ ] **Step 4: Run the test**

Run: `cd api && go test ./internal/service/... -run TestBuildCSV -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/internal/service/tco_service.go api/internal/service/tco_service_test.go
git commit -m "feat(api): assemble vehicle/fleet TCO summaries and CSV export"
```

---

### Task 5: TCO controller + app.go wiring

**Files:**
- Create: `api/internal/controllers/tco.go`
- Modify: `api/internal/app/app.go`

**Interfaces:**
- Consumes: `service.NewTCOService`, `service.TCOSettings`, `service.VehicleTCOSummary`, `service.FleetTCOSummary`, `service.BuildCSV` (Tasks 3–4); `controllers.GetTenant(c) (models.Tenant, error)` (existing, `common.go`); `vehicleSvc.GetVehicle` (existing, used for the per-controller `vehicleInTenant` check, matching `documents.go`).
- Produces: routes `GET /tco/settings`, `PUT /tco/settings`, `GET /tco/summary`, `GET /tco/vehicle/:tokenId`, `GET /tco/export.csv`.

- [ ] **Step 1: Write the controller**

```go
// api/internal/controllers/tco.go
package controllers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

type TCOController struct {
	logger     *zerolog.Logger
	tcoSvc     *service.TCOService
	vehicleSvc *service.VehicleService
}

func NewTCOController(logger *zerolog.Logger, tcoSvc *service.TCOService, vehicleSvc *service.VehicleService) *TCOController {
	return &TCOController{logger: logger, tcoSvc: tcoSvc, vehicleSvc: vehicleSvc}
}

// vehicleInTenant reports whether the tokenID is one of the tenant's synced vehicles.
func (t *TCOController) vehicleInTenant(ctx context.Context, tenantID string, tokenID int64) bool {
	_, err := t.vehicleSvc.GetVehicle(ctx, tenantID, tokenID)
	return err == nil
}

// GetSettings — GET /tco/settings?tokenId=N.
func (t *TCOController) GetSettings(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := strconv.ParseInt(c.Query("tokenId"), 10, 64)
	if err != nil || tokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "valid tokenId query param required")
	}
	if !t.vehicleInTenant(c.Context(), tenant.ID, tokenID) {
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}
	settings, err := t.tcoSvc.GetSettings(c.Context(), tenant.ID, tokenID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "get tco settings: "+err.Error())
	}
	return c.JSON(settings)
}

// PutSettingsRequest is the body for PUT /tco/settings.
type PutSettingsRequest struct {
	TokenID         int64    `json:"tokenId"`
	PurchasePrice   *float64 `json:"purchasePrice"`
	PurchaseDate    *string  `json:"purchaseDate"`
	UsefulLifeYears *int     `json:"usefulLifeYears"`
	Currency        string   `json:"currency"`
}

// PutSettings — PUT /tco/settings.
func (t *TCOController) PutSettings(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	var req PutSettingsRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body: "+err.Error())
	}
	if req.TokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "tokenId is required")
	}
	if !t.vehicleInTenant(c.Context(), tenant.ID, req.TokenID) {
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}
	currency := req.Currency
	if currency == "" {
		currency = "USD"
	}
	settings := service.TCOSettings{
		VehicleTokenID:  req.TokenID,
		PurchasePrice:   req.PurchasePrice,
		PurchaseDate:    req.PurchaseDate,
		UsefulLifeYears: req.UsefulLifeYears,
		Currency:        currency,
	}
	if err := t.tcoSvc.UpsertSettings(c.Context(), tenant.ID, req.TokenID, settings); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "save tco settings: "+err.Error())
	}
	return c.JSON(settings)
}

// GetSummary — GET /tco/summary. Fleet-wide rollup.
func (t *TCOController) GetSummary(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	summary, err := t.tcoSvc.FleetSummary(c.Context(), tenant)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "tco summary: "+err.Error())
	}
	return c.JSON(summary)
}

// GetVehicleDetail — GET /tco/vehicle/:tokenId.
func (t *TCOController) GetVehicleDetail(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := strconv.ParseInt(c.Params("tokenId"), 10, 64)
	if err != nil || tokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "valid tokenId path param required")
	}
	if !t.vehicleInTenant(c.Context(), tenant.ID, tokenID) {
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}
	summary, err := t.tcoSvc.VehicleSummary(c.Context(), tenant, tokenID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "tco vehicle summary: "+err.Error())
	}
	return c.JSON(summary)
}

// ExportCSV — GET /tco/export.csv (optionally ?tokenId=N for a single vehicle).
func (t *TCOController) ExportCSV(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	var summaries []service.VehicleTCOSummary
	filename := "tco-export.csv"
	if q := c.Query("tokenId"); q != "" {
		tokenID, err := strconv.ParseInt(q, 10, 64)
		if err != nil || tokenID == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "invalid tokenId")
		}
		if !t.vehicleInTenant(c.Context(), tenant.ID, tokenID) {
			return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
		}
		summary, err := t.tcoSvc.VehicleSummary(c.Context(), tenant, tokenID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "tco vehicle summary: "+err.Error())
		}
		summaries = []service.VehicleTCOSummary{*summary}
		filename = fmt.Sprintf("tco-vehicle-%d.csv", tokenID)
	} else {
		fleet, err := t.tcoSvc.FleetSummary(c.Context(), tenant)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "tco summary: "+err.Error())
		}
		summaries = fleet.Vehicles
	}
	csvText := service.BuildCSV(summaries)
	c.Set(fiber.HeaderContentType, "text/csv")
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
	return c.SendString(csvText)
}
```

- [ ] **Step 2: Wire routes into app.go**

In `api/internal/app/app.go`, immediately after the existing `// Glovebox / documents` block (which ends with `tenantApp.Delete("/documents/:id", documentsCtrl.DeleteDocument)`), add:

```go
	// Total cost of ownership reporting (operating costs from Glovebox
	// documents + optional acquisition/depreciation settings per vehicle).
	tcoSvc := service.NewTCOService(logger, pdb, fetchAPI, authProvider, vehicleSvc)
	tcoCtrl := controllers.NewTCOController(logger, tcoSvc, vehicleSvc)
	tenantApp.Get("/tco/settings", tcoCtrl.GetSettings)
	tenantApp.Put("/tco/settings", tcoCtrl.PutSettings)
	tenantApp.Get("/tco/summary", tcoCtrl.GetSummary)
	tenantApp.Get("/tco/vehicle/:tokenId", tcoCtrl.GetVehicleDetail)
	tenantApp.Get("/tco/export.csv", tcoCtrl.ExportCSV)
```

(`logger`, `pdb`, `fetchAPI`, `authProvider`, and `vehicleSvc` are already in scope at that point in `app.go` — they're the same variables `documentsCtrl` was just constructed with.)

- [ ] **Step 3: Verify it builds**

Run: `cd api && go build ./...`
Expected: exits 0.

- [ ] **Step 4: Run the full Go test suite**

Run: `cd api && make test`
Expected: all tests pass, including the new `tco_service_test.go` cases.

- [ ] **Step 5: Commit**

```bash
git add api/internal/controllers/tco.go api/internal/app/app.go
git commit -m "feat(api): add TCO controller and wire /tco routes"
```

---

### Task 6: Frontend TCO types + service

**Files:**
- Create: `web/src/types/tco.ts`
- Create: `web/src/services/tco-service.ts`

**Interfaces:**
- Consumes: `ApiService.getInstance().get<T>()/.put<T>()` (existing, `web/src/services/api-service.ts`); `TenantService.getInstance().tenantIdHeader()` (existing, `web/src/services/tenant-service.ts`, used identically in `document-service.ts`).
- Produces: `TCOSettings`, `LineItem`, `VehicleTCOSummary`, `FleetTotals`, `FleetTCOSummary` types; `TCOService.getInstance().{getSettings, putSettings, getSummary, getVehicleDetail, exportCsv}`. Tasks 8–10 consume all of these.

- [ ] **Step 1: Write the types (mirrors the Go JSON shapes from Task 4)**

```typescript
// web/src/types/tco.ts
export interface TCOSettings {
    tokenId: number;
    purchasePrice?: number;
    purchaseDate?: string; // YYYY-MM-DD
    usefulLifeYears?: number;
    currency: string;
}

export interface LineItem {
    vehicleTokenId: number;
    vehicleLabel: string;
    vin: string;
    date: string;
    category: string;
    description: string;
    amount: number;
    currency: string;
}

export interface VehicleTCOSummary {
    tokenId: number;
    vehicleLabel: string;
    vin?: string;
    operatingCost: number;
    costByCategory: Record<string, number>;
    acquisitionCost: number;
    depreciationToDate: number;
    totalTco: number;
    settings: TCOSettings;
    lineItems: LineItem[];
}

export interface FleetTotals {
    operatingCost: number;
    acquisitionCost: number;
    depreciationToDate: number;
    totalTco: number;
}

export interface FleetTCOSummary {
    vehicles: VehicleTCOSummary[];
    fleet: FleetTotals;
}
```

- [ ] **Step 2: Write the service**

```typescript
// web/src/services/tco-service.ts
import { ApiService } from './api-service.ts';
import { TenantService } from './tenant-service.ts';
import { FleetTCOSummary, TCOSettings, VehicleTCOSummary } from '../types/tco.ts';

export class TCOService {
    private static instance: TCOService;
    public static getInstance(): TCOService {
        if (!TCOService.instance) {
            TCOService.instance = new TCOService();
        }
        return TCOService.instance;
    }

    /** GET /tco/settings?tokenId=N. */
    getSettings(tokenId: number): Promise<TCOSettings> {
        return ApiService.getInstance().get<TCOSettings>(`/tco/settings?tokenId=${tokenId}`);
    }

    /** PUT /tco/settings. */
    putSettings(settings: TCOSettings): Promise<TCOSettings> {
        return ApiService.getInstance().put<TCOSettings>('/tco/settings', settings);
    }

    /** GET /tco/summary. Fleet-wide rollup. */
    getSummary(): Promise<FleetTCOSummary> {
        return ApiService.getInstance().get<FleetTCOSummary>('/tco/summary');
    }

    /** GET /tco/vehicle/:tokenId. */
    getVehicleDetail(tokenId: number): Promise<VehicleTCOSummary> {
        return ApiService.getInstance().get<VehicleTCOSummary>(`/tco/vehicle/${tokenId}`);
    }

    /** Trigger a browser download of the CSV export. Omit tokenId for the fleet-wide export. */
    async exportCsv(tokenId?: number): Promise<void> {
        const base = ApiService.getInstance().getApiBaseUrl();
        const token = localStorage.getItem('token');
        const qs = tokenId ? `?tokenId=${tokenId}` : '';
        const res = await fetch(`${base}/tco/export.csv${qs}`, {
            headers: {
                ...(token ? { Authorization: `Bearer ${token}` } : {}),
                ...TenantService.getInstance().tenantIdHeader(),
            },
        });
        if (!res.ok) {
            throw new Error(`export failed: ${res.status} ${await res.text()}`);
        }
        const blob = await res.blob();
        const disposition = res.headers.get('Content-Disposition') || '';
        const match = /filename="?([^";]+)"?/i.exec(disposition);
        const filename = match?.[1] || 'tco-export.csv';
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
    }
}
```

- [ ] **Step 3: Verify the TypeScript compiles**

Run: `cd web && tsc --noEmit`
Expected: exits 0.

- [ ] **Step 4: Commit**

```bash
git add web/src/types/tco.ts web/src/services/tco-service.ts
git commit -m "feat(web): add TCO types and API service"
```

---

### Task 7: Fuel category + cost-eligible category set (frontend)

**Files:**
- Modify: `web/src/utils/document-categories.ts`

**Interfaces:**
- Produces: `'dimo.document.vehicle.fuel'` entry in `CE_TYPE_TO_LABEL` and `UPLOAD_CATEGORIES`; `COST_ELIGIBLE_CATEGORIES: Set<string>`. Task 8 consumes `COST_ELIGIBLE_CATEGORIES`; `upload-document-modal.ts` and `tco-view.ts` consume the label/category additions.

- [ ] **Step 1: Add the Fuel category and the cost-eligible set**

```typescript
// web/src/utils/document-categories.ts — full new contents

/**
 * Map of canonical CE types → friendly UI labels.
 * Source of truth for both upload (category dropdown) and list (group headers).
 */
export const CE_TYPE_TO_LABEL: Record<string, string> = {
    'dimo.document.vehicle.service.invoice': 'Service & parts',
    'dimo.document.vehicle.insurance':       'Insurance',
    'dimo.document.vehicle.registration':    'Registration',
    'dimo.document.vehicle.inspection':      'Inspection',
    'dimo.document.vehicle.title':           'Title',
    'dimo.document.vehicle.finance':         'Finance',
    'dimo.document.vehicle.regulatory.other':'Regulatory',
    'dimo.document.vehicle.maintenance':     'Service & parts',
    'dimo.document.vehicle.note':            'Note',
    'dimo.document.vehicle.expense':         'Other',
    'dimo.document.vehicle.condition':       'Other',
    'dimo.document.vehicle.fuel':            'Fuel',
    'dimo.document.unknown':                 'Uncategorized',
};

/**
 * Choices shown in the upload modal's category dropdown.
 * Order is the same order they'll appear to the user.
 */
export const UPLOAD_CATEGORIES: Array<{ ceType: string; label: string }> = [
    { ceType: 'dimo.document.vehicle.insurance',       label: 'Insurance' },
    { ceType: 'dimo.document.vehicle.registration',    label: 'Registration' },
    { ceType: 'dimo.document.vehicle.inspection',      label: 'Inspection' },
    { ceType: 'dimo.document.vehicle.service.invoice', label: 'Service & parts' },
    { ceType: 'dimo.document.vehicle.fuel',            label: 'Fuel' },
    { ceType: 'dimo.document.vehicle.title',           label: 'Title' },
    { ceType: 'dimo.document.vehicle.finance',         label: 'Finance' },
    { ceType: 'dimo.document.vehicle.regulatory.other',label: 'Other regulatory' },
    { ceType: 'dimo.document.unknown',                 label: 'Other' },
];

/**
 * CE types we expect a vehicle owner to keep on file. Used by the glovebox
 * "Missing" rail — anything in this set that the vehicle does not have an
 * attestation for shows as a prompt to add it.
 */
export const EXPECTED_CE_TYPES: string[] = [
    'dimo.document.vehicle.insurance',
    'dimo.document.vehicle.registration',
    'dimo.document.vehicle.inspection',
];

/**
 * CE types eligible to carry a cost amount, and to count toward TCO operating
 * costs. Mirrors the backend's CostEligibleCETypes (api/internal/service/tco_service.go).
 * Everything except Note/Condition/Title.
 */
export const COST_ELIGIBLE_CATEGORIES = new Set<string>([
    'dimo.document.vehicle.service.invoice',
    'dimo.document.vehicle.insurance',
    'dimo.document.vehicle.registration',
    'dimo.document.vehicle.inspection',
    'dimo.document.vehicle.finance',
    'dimo.document.vehicle.regulatory.other',
    'dimo.document.vehicle.maintenance',
    'dimo.document.vehicle.expense',
    'dimo.document.vehicle.fuel',
]);

export function categoryLabel(ceType: string): string {
    return CE_TYPE_TO_LABEL[ceType] ?? 'Other';
}
```

- [ ] **Step 2: Verify the TypeScript compiles**

Run: `cd web && tsc --noEmit`
Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add web/src/utils/document-categories.ts
git commit -m "feat(web): add Fuel document category and cost-eligible category set"
```

---

### Task 8: Amount field in upload-confirm modal

**Files:**
- Modify: `web/src/elements/upload-document-modal.ts`

**Interfaces:**
- Consumes: `COST_ELIGIBLE_CATEGORIES` (Task 7).
- Produces: `parsedData.amount` (number) and `parsedData.currency` (string, `"USD"`) included in the `POST /documents/attest` body when the user enters an amount for a cost-eligible category — consumed server-side by `extractAmount` (Task 2) when building TCO summaries.

- [ ] **Step 1: Import the cost-eligible set and add amount state**

In `web/src/elements/upload-document-modal.ts`, change the import line:

```typescript
import { UPLOAD_CATEGORIES, categoryLabel, COST_ELIGIBLE_CATEGORIES } from '../utils/document-categories.ts';
```

Add a new state field alongside the existing ones:

```typescript
    @state() private amount: string = '';
```

- [ ] **Step 2: Render the Amount field in the confirm step**

In `renderReview()`, insert this block immediately after the closing `</div>` of the `.field` for Category (i.e. right before the `${this.errorMessage ? ... }` line):

```typescript
            ${COST_ELIGIBLE_CATEGORIES.has(this.selectedCategory) ? html`
                <div class="field">
                    <label for="amount">${msg('Amount (optional)')}</label>
                    <input
                        id="amount"
                        type="text"
                        inputmode="decimal"
                        placeholder="0.00"
                        .value=${this.amount}
                        @input=${(e: Event) => { this.amount = (e.target as HTMLInputElement).value; }}
                    />
                </div>
            ` : nothing}
```

Add a matching CSS rule next to the existing `.field select, .field input[type="text"]` rule — it already targets `input[type="text"]`, so no new CSS is needed.

- [ ] **Step 3: Include the amount in the attest request**

In `onConfirm()`, change the `parsedData` line from:

```typescript
                parsedData: this.extractResult?.fields || {},
```

to:

```typescript
                parsedData: this.buildParsedData(),
```

and add this new private method right above `onConfirm()`:

```typescript
    private buildParsedData(): Record<string, unknown> {
        const base = { ...(this.extractResult?.fields || {}) };
        const trimmed = this.amount.trim();
        if (COST_ELIGIBLE_CATEGORIES.has(this.selectedCategory) && trimmed !== '') {
            const parsed = Number(trimmed);
            if (!Number.isNaN(parsed)) {
                base.amount = parsed;
                base.currency = 'USD';
            }
        }
        return base;
    }
```

- [ ] **Step 4: Verify the TypeScript compiles**

Run: `cd web && tsc --noEmit`
Expected: exits 0.

- [ ] **Step 5: Manual verification**

Run: `cd web && npm run dev` (and `cd api && go run ./cmd/fleet-lite-app` if the API isn't already running). Open the app in Chrome, go to Glovebox, click "+", pick a file, confirm the category is "Service & parts" (or another cost-eligible category), verify the "Amount (optional)" field appears, enter `123.45`, save. Confirm no console errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/elements/upload-document-modal.ts
git commit -m "feat(web): capture an optional dollar amount at document upload confirm"
```

---

### Task 9: TCO fleet view — nav, route, and fleet-wide table

**Files:**
- Create: `web/src/views/tco-view.ts`
- Modify: `web/src/views/index.ts`
- Modify: `web/src/elements/side-nav.ts`
- Modify: `web/src/elements/app-root.ts`

**Interfaces:**
- Consumes: `TCOService.getInstance().getSummary()/.exportCsv()` (Task 6); `Vehicle`-style formatting conventions already used in `glovebox.ts`.
- Produces: `<tco-view>` custom element rendering the fleet table (consumed as the `/:tenantId/tco` route render target); Task 10 extends this same file with the per-vehicle drilldown.

- [ ] **Step 1: Write the fleet-table view**

```typescript
// web/src/views/tco-view.ts
import { LitElement, html, css, nothing } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { TCOService } from '../services/tco-service.ts';
import { FleetTCOSummary, VehicleTCOSummary } from '../types/tco.ts';

function formatMoney(n: number): string {
    return n.toLocaleString(undefined, { style: 'currency', currency: 'USD' });
}

@customElement('tco-view')
export class TCOView extends LitElement {
    @property({ type: String }) tenantId = '';

    @state() private loading = true;
    @state() private error = '';
    @state() private summary: FleetTCOSummary | null = null;
    @state() private detailTokenId: number | null = null;
    @state() private exporting = false;

    static styles = [
        sharedStyles,
        css`
            :host {
                display: block;
                width: 100%;
                height: 100%;
                overflow-y: auto;
                padding: var(--stack-lg) var(--gutter);
            }
            .header {
                display: flex;
                align-items: center;
                justify-content: space-between;
                margin-bottom: var(--stack-lg);
            }
            .header h1 { font: var(--type-headline-lg); color: var(--primary); }
            button.export {
                padding: 10px 18px;
                border-radius: var(--radius-md);
                background: var(--primary);
                color: var(--on-primary);
                border: none;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                font-weight: 700;
                cursor: pointer;
            }
            button.export:disabled { opacity: 0.5; cursor: not-allowed; }
            table { width: 100%; border-collapse: collapse; }
            th, td {
                text-align: left;
                padding: 12px 16px;
                border-bottom: 1px solid var(--outline-variant);
                font: var(--type-body-sm);
            }
            th {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            tbody tr { cursor: pointer; }
            tbody tr:hover { background: var(--surface-container-low); }
            td.num { text-align: right; font-variant-numeric: tabular-nums; }
            .empty, .loading, .error {
                padding: 48px 0;
                text-align: center;
                color: var(--on-surface-variant);
            }
            .error { color: var(--error); }
            tfoot td { font-weight: 700; border-top: 2px solid var(--outline-variant); border-bottom: none; }
        `,
    ];

    async connectedCallback() {
        super.connectedCallback();
        await this.loadSummary();
    }

    private async loadSummary() {
        this.loading = true;
        this.error = '';
        try {
            this.summary = await TCOService.getInstance().getSummary();
        } catch (e) {
            console.error('Failed to load TCO summary', e);
            this.error = e instanceof Error ? e.message : msg('Failed to load TCO summary');
        } finally {
            this.loading = false;
        }
    }

    private openVehicle(v: VehicleTCOSummary) {
        this.detailTokenId = v.tokenId;
    }

    private closeDetail = async () => {
        this.detailTokenId = null;
        await this.loadSummary();
    };

    private async exportFleetCsv() {
        this.exporting = true;
        try {
            await TCOService.getInstance().exportCsv();
        } catch (e) {
            console.error('Failed to export TCO CSV', e);
        } finally {
            this.exporting = false;
        }
    }

    private renderFleetTable() {
        const summary = this.summary!;
        if (summary.vehicles.length === 0) {
            return html`<div class="empty">${msg('No vehicles on this account.')}</div>`;
        }
        return html`
            <table>
                <thead>
                    <tr>
                        <th>${msg('Vehicle')}</th>
                        <th class="num">${msg('Operating cost')}</th>
                        <th class="num">${msg('Acquisition')}</th>
                        <th class="num">${msg('Depreciation to date')}</th>
                        <th class="num">${msg('Total TCO')}</th>
                    </tr>
                </thead>
                <tbody>
                    ${summary.vehicles.map((v) => html`
                        <tr @click=${() => this.openVehicle(v)}>
                            <td>${v.vehicleLabel}</td>
                            <td class="num">${formatMoney(v.operatingCost)}</td>
                            <td class="num">${formatMoney(v.acquisitionCost)}</td>
                            <td class="num">${formatMoney(v.depreciationToDate)}</td>
                            <td class="num">${formatMoney(v.totalTco)}</td>
                        </tr>
                    `)}
                </tbody>
                <tfoot>
                    <tr>
                        <td>${msg('Fleet total')}</td>
                        <td class="num">${formatMoney(summary.fleet.operatingCost)}</td>
                        <td class="num">${formatMoney(summary.fleet.acquisitionCost)}</td>
                        <td class="num">${formatMoney(summary.fleet.depreciationToDate)}</td>
                        <td class="num">${formatMoney(summary.fleet.totalTco)}</td>
                    </tr>
                </tfoot>
            </table>
        `;
    }

    render() {
        return html`
            <div class="header">
                <h1>${msg('Total Cost of Ownership')}</h1>
                <button class="export" ?disabled=${this.exporting || this.loading} @click=${() => this.exportFleetCsv()}>
                    ${this.exporting ? msg('Exporting…') : msg('Export CSV')}
                </button>
            </div>
            ${this.loading
                ? html`<div class="loading">${msg('Loading…')}</div>`
                : this.error
                    ? html`<div class="error">${this.error}</div>`
                    : this.summary
                        ? this.renderFleetTable()
                        : nothing
            }
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'tco-view': TCOView;
    }
}
```

- [ ] **Step 2: Export the new view**

In `web/src/views/index.ts`, add:

```typescript
export * from './tco-view.ts';
```

- [ ] **Step 3: Add the nav entry**

In `web/src/elements/side-nav.ts`, change the `NavKey` type to:

```typescript
type NavKey = 'vehicles' | 'stats' | 'groups' | 'geofences' | 'glovebox' | 'tco' | 'settings';
```

and add an entry to `ITEMS` (right after the `glovebox` entry):

```typescript
    { key: 'tco',      icon: 'payments',       label: () => msg('TCO'),      suffix: '/tco' },
```

- [ ] **Step 4: Wire the route in app-root.ts**

In `web/src/elements/app-root.ts`:

Add the import next to the other view imports:

```typescript
import '../views/tco-view.ts';
```

Update the `NavKey` type (must match `side-nav.ts`'s):

```typescript
type NavKey = 'vehicles' | 'stats' | 'groups' | 'geofences' | 'glovebox' | 'tco' | 'settings';
```

Add a route entry in the `Routes` constructor, right after the `glovebox` routes:

```typescript
            { path: '/:tenantId/tco',                 render: () => html`<tco-view .tenantId=${this.tenantId}></tco-view>` },
```

Add a branch in `deriveActive()`, right after the `glovebox` check:

```typescript
        if (path.startsWith('/tco')) return 'tco';
```

- [ ] **Step 5: Verify the TypeScript compiles**

Run: `cd web && tsc --noEmit`
Expected: exits 0.

- [ ] **Step 6: Manual verification**

Run: `cd web && npm run dev` (with the API running). Open the app in Chrome, confirm a "TCO" item appears in the side nav, click it, confirm the fleet table loads (or shows the empty state / error state correctly), and confirm "Export CSV" downloads a `tco-export.csv` file with a header row.

- [ ] **Step 7: Commit**

```bash
git add web/src/views/tco-view.ts web/src/views/index.ts web/src/elements/side-nav.ts web/src/elements/app-root.ts
git commit -m "feat(web): add TCO nav tab, route, and fleet-wide cost table"
```

---

### Task 10: TCO vehicle drilldown — breakdown, line items, acquisition settings, single-vehicle export

**Files:**
- Modify: `web/src/views/tco-view.ts`

**Interfaces:**
- Consumes: `TCOService.getInstance().getVehicleDetail()/.putSettings()/.exportCsv(tokenId)` (Task 6); `categoryLabel()` (Task 7); `detailTokenId`, `closeDetail`, `renderFleetTable`, styles (Task 9 — same file).
- Produces: full drilldown UI swapped in when `detailTokenId` is set; no new exports (still just `<tco-view>`).

- [ ] **Step 1: Add drilldown state and data loading**

In `web/src/views/tco-view.ts`, add the `categoryLabel` import:

```typescript
import { categoryLabel } from '../utils/document-categories.ts';
```

Add new state fields alongside the existing ones:

```typescript
    @state() private detail: VehicleTCOSummary | null = null;
    @state() private loadingDetail = false;
    @state() private detailError = '';
    @state() private exportingVehicle = false;
    @state() private savingSettings = false;
    @state() private settingsError = '';
    @state() private formPurchasePrice = '';
    @state() private formPurchaseDate = '';
    @state() private formUsefulLifeYears = '';
```

Replace the `openVehicle` method with one that loads the detail:

```typescript
    private async openVehicle(v: VehicleTCOSummary) {
        this.detailTokenId = v.tokenId;
        this.loadingDetail = true;
        this.detailError = '';
        try {
            this.detail = await TCOService.getInstance().getVehicleDetail(v.tokenId);
            this.formPurchasePrice = this.detail.settings.purchasePrice?.toString() ?? '';
            this.formPurchaseDate = this.detail.settings.purchaseDate ?? '';
            this.formUsefulLifeYears = this.detail.settings.usefulLifeYears?.toString() ?? '';
        } catch (e) {
            console.error('Failed to load vehicle TCO detail', e);
            this.detailError = e instanceof Error ? e.message : msg('Failed to load vehicle detail');
        } finally {
            this.loadingDetail = false;
        }
    }
```

Update `closeDetail` to also clear the new state:

```typescript
    private closeDetail = async () => {
        this.detailTokenId = null;
        this.detail = null;
        await this.loadSummary();
    };
```

- [ ] **Step 2: Add the settings-save and single-vehicle export methods**

Add these methods next to `exportFleetCsv`:

```typescript
    private async saveSettings() {
        if (this.detailTokenId === null) return;
        this.savingSettings = true;
        this.settingsError = '';
        try {
            const price = this.formPurchasePrice.trim() === '' ? undefined : Number(this.formPurchasePrice);
            const life = this.formUsefulLifeYears.trim() === '' ? undefined : Number(this.formUsefulLifeYears);
            await TCOService.getInstance().putSettings({
                tokenId: this.detailTokenId,
                purchasePrice: price !== undefined && !Number.isNaN(price) ? price : undefined,
                purchaseDate: this.formPurchaseDate.trim() === '' ? undefined : this.formPurchaseDate,
                usefulLifeYears: life !== undefined && !Number.isNaN(life) ? life : undefined,
                currency: 'USD',
            });
            this.detail = await TCOService.getInstance().getVehicleDetail(this.detailTokenId);
        } catch (e) {
            console.error('Failed to save TCO settings', e);
            this.settingsError = e instanceof Error ? e.message : msg('Failed to save');
        } finally {
            this.savingSettings = false;
        }
    }

    private async exportVehicleCsv() {
        if (this.detailTokenId === null) return;
        this.exportingVehicle = true;
        try {
            await TCOService.getInstance().exportCsv(this.detailTokenId);
        } catch (e) {
            console.error('Failed to export vehicle TCO CSV', e);
        } finally {
            this.exportingVehicle = false;
        }
    }
```

- [ ] **Step 3: Add drilldown styles**

Append to the `styles` array's `css` template (inside the existing backtick block, after the `tfoot td` rule):

```css
            .back { background: none; border: none; color: var(--on-surface-variant); cursor: pointer; margin-bottom: var(--stack-md); font: var(--type-body-sm); display: flex; align-items: center; gap: 4px; }
            .back:hover { color: var(--primary); }
            .breakdown { display: flex; flex-wrap: wrap; gap: 16px; margin-bottom: var(--stack-lg); }
            .stat { background: var(--surface-container-low); border: 1px solid var(--outline-variant); border-radius: var(--radius-md); padding: 16px; min-width: 160px; }
            .stat .label { font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; color: var(--on-surface-variant); margin-bottom: 4px; }
            .stat .value { font: var(--type-headline-md); color: var(--primary); }
            .settings-form { display: flex; flex-wrap: wrap; gap: 16px; align-items: flex-end; margin-bottom: var(--stack-lg); padding: 16px; background: var(--surface-container-low); border: 1px solid var(--outline-variant); border-radius: var(--radius-md); }
            .settings-form .field { display: flex; flex-direction: column; gap: 4px; }
            .settings-form label { font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; color: var(--on-surface-variant); }
            .settings-form input { background: var(--surface-container); color: var(--on-surface); border: 1px solid var(--outline-variant); border-radius: var(--radius-md); padding: 8px 10px; font-family: inherit; }
            .settings-form button { padding: 10px 16px; border-radius: var(--radius-md); background: var(--primary); color: var(--on-primary); border: none; font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; font-weight: 700; cursor: pointer; }
            .settings-form button:disabled { opacity: 0.5; cursor: not-allowed; }
```

- [ ] **Step 4: Render the drilldown**

Add this new render method, right before `render()`:

```typescript
    private renderDetail() {
        if (this.loadingDetail) return html`<div class="loading">${msg('Loading…')}</div>`;
        if (this.detailError) return html`<div class="error">${this.detailError}</div>`;
        const d = this.detail;
        if (!d) return nothing;
        const categories = Object.entries(d.costByCategory).sort((a, b) => b[1] - a[1]);
        return html`
            <button class="back" @click=${this.closeDetail}>
                <span class="material-symbols-outlined">arrow_back</span>
                ${msg('Back to fleet')}
            </button>
            <div class="header">
                <h1>${d.vehicleLabel}</h1>
                <button class="export" ?disabled=${this.exportingVehicle} @click=${() => this.exportVehicleCsv()}>
                    ${this.exportingVehicle ? msg('Exporting…') : msg('Export CSV')}
                </button>
            </div>

            <div class="breakdown">
                <div class="stat"><div class="label">${msg('Operating cost')}</div><div class="value">${formatMoney(d.operatingCost)}</div></div>
                <div class="stat"><div class="label">${msg('Acquisition')}</div><div class="value">${formatMoney(d.acquisitionCost)}</div></div>
                <div class="stat"><div class="label">${msg('Depreciation to date')}</div><div class="value">${formatMoney(d.depreciationToDate)}</div></div>
                <div class="stat"><div class="label">${msg('Total TCO')}</div><div class="value">${formatMoney(d.totalTco)}</div></div>
                ${categories.map(([cat, amount]) => html`
                    <div class="stat"><div class="label">${categoryLabel(cat)}</div><div class="value">${formatMoney(amount)}</div></div>
                `)}
            </div>

            <div class="settings-form">
                <div class="field">
                    <label for="price">${msg('Purchase price')}</label>
                    <input id="price" type="text" inputmode="decimal" placeholder="0.00"
                        .value=${this.formPurchasePrice}
                        @input=${(e: Event) => { this.formPurchasePrice = (e.target as HTMLInputElement).value; }} />
                </div>
                <div class="field">
                    <label for="date">${msg('Purchase date')}</label>
                    <input id="date" type="date"
                        .value=${this.formPurchaseDate}
                        @input=${(e: Event) => { this.formPurchaseDate = (e.target as HTMLInputElement).value; }} />
                </div>
                <div class="field">
                    <label for="life">${msg('Useful life (years)')}</label>
                    <input id="life" type="text" inputmode="numeric" placeholder="10"
                        .value=${this.formUsefulLifeYears}
                        @input=${(e: Event) => { this.formUsefulLifeYears = (e.target as HTMLInputElement).value; }} />
                </div>
                <button ?disabled=${this.savingSettings} @click=${() => this.saveSettings()}>
                    ${this.savingSettings ? msg('Saving…') : msg('Save')}
                </button>
                ${this.settingsError ? html`<span class="error">${this.settingsError}</span>` : nothing}
            </div>

            <table>
                <thead>
                    <tr>
                        <th>${msg('Date')}</th>
                        <th>${msg('Category')}</th>
                        <th>${msg('Description')}</th>
                        <th class="num">${msg('Amount')}</th>
                    </tr>
                </thead>
                <tbody>
                    ${d.lineItems.map((li) => html`
                        <tr>
                            <td>${new Date(li.date).toLocaleDateString()}</td>
                            <td>${categoryLabel(li.category)}</td>
                            <td>${li.description}</td>
                            <td class="num">${formatMoney(li.amount)}</td>
                        </tr>
                    `)}
                </tbody>
            </table>
        `;
    }
```

- [ ] **Step 5: Swap in the drilldown from the top-level `render()`**

Replace the existing `render()` method's body with a check for `detailTokenId`:

```typescript
    render() {
        if (this.detailTokenId !== null) {
            return this.renderDetail();
        }
        return html`
            <div class="header">
                <h1>${msg('Total Cost of Ownership')}</h1>
                <button class="export" ?disabled=${this.exporting || this.loading} @click=${() => this.exportFleetCsv()}>
                    ${this.exporting ? msg('Exporting…') : msg('Export CSV')}
                </button>
            </div>
            ${this.loading
                ? html`<div class="loading">${msg('Loading…')}</div>`
                : this.error
                    ? html`<div class="error">${this.error}</div>`
                    : this.summary
                        ? this.renderFleetTable()
                        : nothing
            }
        `;
    }
```

- [ ] **Step 6: Verify the TypeScript compiles**

Run: `cd web && tsc --noEmit`
Expected: exits 0.

- [ ] **Step 7: Manual verification**

Run: `cd web && npm run dev` (with the API running). In the TCO tab, click a vehicle row. Confirm the drilldown shows the category breakdown, an empty line-item table (or populated, if you completed Task 8's manual test), and the acquisition settings form. Enter a purchase price (`25000`), date, and useful life (`8`), click Save, confirm the stats update. Click "Export CSV" and confirm a `tco-vehicle-<id>.csv` downloads. Click "Back to fleet" and confirm the fleet table reloads with updated acquisition/depreciation numbers for that vehicle.

- [ ] **Step 8: Commit**

```bash
git add web/src/views/tco-view.ts
git commit -m "feat(web): add TCO vehicle drilldown with acquisition settings and per-vehicle export"
```

---

## Self-Review Notes

- **Spec coverage:** operating costs from documents (Tasks 2, 4, 8), acquisition + straight-line depreciation (Tasks 1, 3, 4, 10), fleet-wide + drilldown UI (Tasks 9, 10), CSV export both scopes (Tasks 4, 5, 9, 10), Fuel category (Task 7), cost-eligible category list (Tasks 2, 7), all-time-only scope (no date filtering added anywhere, matches spec). All spec sections have a task.
- **Type consistency checked:** Go `TCOSettings`/`VehicleTCOSummary`/`FleetTCOSummary`/`LineItem` JSON tags match the TS `TCOSettings`/`VehicleTCOSummary`/`FleetTCOSummary`/`LineItem` interfaces field-for-field (Task 6 mirrors Task 3/4's JSON tags exactly). `categoryLabelForCSV` (Go, Task 4) and `categoryLabel`/`CE_TYPE_TO_LABEL` (TS, Task 7) are intentionally-duplicated mirrors, called out in a comment.
- **No placeholders:** every step has complete, runnable code; no "TODO" or "similar to Task N" shortcuts.

