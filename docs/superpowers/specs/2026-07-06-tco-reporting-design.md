# TCO Reporting — Design

Created: 2026-07-06

## Goal

Add a "Total Cost of Ownership" (TCO) tab that turns Glovebox documents
(service invoices, insurance, registration, fuel receipts, etc.) into a cost
analysis per vehicle and across the fleet, including depreciation, with CSV
export.

## Scope

**In scope:**
- Operating costs derived from dollar amounts attached to uploaded Glovebox
  documents.
- One-time acquisition cost (purchase price/date) per vehicle, entered
  manually.
- Straight-line depreciation from acquisition data.
- Fleet-wide summary view with per-vehicle drilldown.
- CSV export (line-item, both fleet-wide and single-vehicle).
- All-time totals only — no date-range filtering in v1.

**Out of scope (this round):**
- Declining-balance or market-value-based depreciation.
- Editing a document's amount after the initial upload/confirm step.
- Date-range/year filtering in the report UI.
- Any change to how documents are stored (still DIS CloudEvents via
  extract→confirm→attest, no new document storage system).

## Data model

**New Postgres table** `vehicle_tco_settings` (new migration, SQLBoiler
regenerated, follows existing `db/migrations` + `db/models` pattern):

```sql
tenant_id         text
vehicle_token_id  bigint
purchase_price    numeric
purchase_date     date
useful_life_years int
currency          text default 'USD'
created_at / updated_at
```

One optional row per vehicle. No row = operating-costs-only view, no
depreciation/acquisition line.

**New document category**: `dimo.document.vehicle.fuel` → "Fuel", added to
`CE_TYPE_TO_LABEL` (`web/src/utils/document-categories.ts`) and the upload
modal's category picker.

**Amount on documents**: no DIS schema change. `amount` and `currency` become
additional keys in the existing `fields`/`parsedData` map already sent to
`POST /documents/attest`. Populated via a new "Amount" input added to the
upload-confirm step, for every category except Note/Condition/Title.

**Cost-eligible categories** (count toward TCO operating costs): Service &
parts, Insurance, Registration, Inspection, Finance, Regulatory, Expense,
Fuel. Excluded: Note, Condition, Title.

## Backend (Go)

New service `internal/service/tco_service.go`: aggregation loop over a
tenant's vehicles (reusing existing fetch-api list logic), sums `amount` by
category, computes straight-line depreciation
(`(price − 0) / usefulLifeYears × yearsElapsed`, capped at `price`). Shared by
all endpoints below so JSON and CSV numbers can't drift apart.

| Route | Purpose |
|---|---|
| `GET /tco/settings?tokenId=N` | Fetch a vehicle's acquisition settings (empty/404 if none) |
| `PUT /tco/settings` | Upsert `{tokenId, purchasePrice, purchaseDate, usefulLifeYears, currency}` |
| `GET /tco/summary` | Fleet-wide rollup: per-vehicle totals (operating cost by category, acquisition, depreciation-to-date, total TCO) + fleet total |
| `GET /tco/vehicle/:tokenId` | Single-vehicle detail: same shape as summary for one vehicle, plus full line-item list (date, category, filename, amount) |
| `GET /tco/export.csv` (optional `?tokenId=N`) | Streams line-item CSV, fleet-wide or single-vehicle |

`documents.go`'s `AttestDocument` handler needs no changes — `amount`/
`currency` just ride along in the existing `parsedData` body field, and
"Fuel" is a valid category value.

## Frontend (Lit)

- New side-nav entry "TCO" → new view `web/src/views/tco-view.ts`.
- Fleet-wide table: one row per vehicle (operating cost, acquisition,
  depreciation-to-date, total TCO), sortable, "Export CSV" button.
- Row click → drilldown: category breakdown, acquisition/depreciation
  edit panel (price, date, useful-life-years, save), full line-item table,
  its own single-vehicle "Export CSV" button.
- New service `web/src/services/tco-service.ts`: typed wrappers for the four
  `/tco/*` endpoints.
- `upload-document-modal.ts`: add editable "Amount" field to the confirm
  step (for cost-eligible categories) and "Fuel" to the category picker.

## CSV export format

One row per document, plus trailing per-vehicle summary rows for
acquisition/depreciation (not documents, but kept in the same flat table):

```
vehicle,vin,date,category,description,amount,currency
2021 Subaru Ascent,1HGCM82633A123456,2026-03-14,Service & parts,invoice_march.pdf,412.50,USD
2021 Subaru Ascent,1HGCM82633A123456,(acquisition),Acquisition,Purchase price,28500.00,USD
2021 Subaru Ascent,1HGCM82633A123456,(acquisition),Depreciation,Depreciation to date,-6200.00,USD
```

## Testing

- Go unit tests for `tco_service.go`: depreciation math edge cases (no
  purchase date, useful life 0, fully depreciated), category sum-by-bucket
  logic. Follows existing `*_test.go` conventions (e.g.
  `geofence_detection_test.go`).
- Manual Chrome verification: upload a document with an amount, confirm it
  surfaces in the TCO drilldown, confirm CSV export downloads and opens
  correctly for both fleet-wide and single-vehicle.
- No new external integrations, so no new contract/integration tests beyond
  existing fetch-api/attest-api coverage.
