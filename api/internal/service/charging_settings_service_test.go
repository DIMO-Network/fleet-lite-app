// api/internal/service/charging_settings_service_test.go
package service

import "testing"

func TestComputeSessionCost_FullSettings(t *testing.T) {
	energy := 30.0
	settings := ChargingSettings{
		ElectricityRate:   f64ptr(0.15),
		GasPrice:          f64ptr(3.50),
		GasMpgEquivalent:  f64ptr(30),
		VehicleKwhPerMile: f64ptr(0.30),
	}
	got := computeSessionCost(&energy, settings)
	if got.Cost == nil || *got.Cost != 4.5 {
		t.Fatalf("cost = %v, want 4.5", got.Cost)
	}
	// miles enabled = 30/0.30 = 100; gallons avoided = 100/30 = 3.3333...;
	// gas cost avoided = 3.3333... * 3.50 = 11.6666...
	if got.GasCostAvoided == nil || round2(*got.GasCostAvoided) != 11.67 {
		t.Fatalf("gasCostAvoided = %v, want ~11.67", got.GasCostAvoided)
	}
	if got.Savings == nil || round2(*got.Savings) != 7.17 {
		t.Fatalf("savings = %v, want ~7.17", got.Savings)
	}
}

func TestComputeSessionCost_NoSettings(t *testing.T) {
	energy := 30.0
	got := computeSessionCost(&energy, ChargingSettings{})
	if got.Cost != nil || got.GasCostAvoided != nil || got.Savings != nil {
		t.Fatalf("got %+v, want all nil (no settings configured)", got)
	}
}

func TestComputeSessionCost_NilEnergy(t *testing.T) {
	settings := ChargingSettings{
		ElectricityRate:   f64ptr(0.15),
		GasPrice:          f64ptr(3.50),
		GasMpgEquivalent:  f64ptr(30),
		VehicleKwhPerMile: f64ptr(0.30),
	}
	got := computeSessionCost(nil, settings)
	if got.Cost != nil || got.GasCostAvoided != nil || got.Savings != nil {
		t.Fatalf("got %+v, want all nil (no energy reading)", got)
	}
}

func TestComputeSessionCost_ZeroEfficiencyDoesNotDivideByZero(t *testing.T) {
	energy := 30.0
	settings := ChargingSettings{
		ElectricityRate:   f64ptr(0.15),
		GasPrice:          f64ptr(3.50),
		GasMpgEquivalent:  f64ptr(30),
		VehicleKwhPerMile: f64ptr(0), // misconfigured tenant setting
	}
	got := computeSessionCost(&energy, settings)
	if got.Cost == nil || *got.Cost != 4.5 {
		t.Fatalf("cost = %v, want 4.5 (electricity cost still computable)", got.Cost)
	}
	if got.GasCostAvoided != nil || got.Savings != nil {
		t.Fatalf("got gasCostAvoided=%v savings=%v, want nil (efficiency is 0)", got.GasCostAvoided, got.Savings)
	}
}

func TestComputeSessionCost_ZeroMpgDoesNotDivideByZero(t *testing.T) {
	energy := 30.0
	settings := ChargingSettings{
		ElectricityRate:   f64ptr(0.15),
		GasPrice:          f64ptr(3.50),
		GasMpgEquivalent:  f64ptr(0), // misconfigured tenant setting
		VehicleKwhPerMile: f64ptr(0.30),
	}
	got := computeSessionCost(&energy, settings)
	if got.GasCostAvoided != nil || got.Savings != nil {
		t.Fatalf("got gasCostAvoided=%v savings=%v, want nil (mpg is 0)", got.GasCostAvoided, got.Savings)
	}
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
