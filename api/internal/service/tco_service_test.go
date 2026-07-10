// api/internal/service/tco_service_test.go
package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
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
			if diff := got - tc.want; diff > 15 || diff < -15 {
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

func TestCostAmendments(t *testing.T) {
	entries := []gateway.AttestationEntry{
		{
			ID:   "amend-1",
			Type: "dimo.document.vehicle.cost-amendment",
			Data: json.RawMessage(`{"documentId":"doc-1","amount":150,"currency":"EUR"}`),
		},
		{
			ID:   "amend-2",
			Type: "dimo.document.vehicle.cost-amendment",
			Data: json.RawMessage(`{"documentId":"doc-2","amount":75}`),
		},
		{
			ID:   "amend-3-tombstoned",
			Type: "dimo.document.vehicle.cost-amendment",
			Data: json.RawMessage(`{"documentId":"doc-3","amount":999}`),
		},
		{
			ID:   "not-an-amendment",
			Type: "dimo.document.vehicle.insurance",
			Data: json.RawMessage(`{"amount":10}`),
		},
	}
	tombstoned := map[string]struct{}{"amend-3-tombstoned": {}}

	got := costAmendments(entries, tombstoned)

	if len(got) != 2 {
		t.Fatalf("expected 2 amendments, got %d: %+v", len(got), got)
	}
	if am := got["doc-1"]; am.amount != 150 || am.currency != "EUR" {
		t.Fatalf("doc-1 amendment = %+v, want {150 EUR}", am)
	}
	if am := got["doc-2"]; am.amount != 75 || am.currency != "USD" {
		t.Fatalf("doc-2 amendment (currency default) = %+v, want {75 USD}", am)
	}
	if _, ok := got["doc-3"]; ok {
		t.Fatalf("tombstoned amendment for doc-3 should be excluded")
	}
}
