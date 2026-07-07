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
