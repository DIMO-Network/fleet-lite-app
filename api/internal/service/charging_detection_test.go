// api/internal/service/charging_detection_test.go
package service

import (
	"testing"
	"time"
)

func chargingSample(sec int, charging *bool, addedEnergy, power, soc, lat, lng *float64) ChargingSample {
	return ChargingSample{
		Time:           time.Date(2026, 9, 1, 8, 0, sec, 0, time.UTC),
		IsCharging:     charging,
		AddedEnergyKwh: addedEnergy,
		PowerKw:        power,
		SocPct:         soc,
		Lat:            lat,
		Lng:            lng,
	}
}

func bptr(b bool) *bool       { return &b }
func f64ptr(f float64) *float64 { return &f }

func TestDetectChargingSessions_SingleSession(t *testing.T) {
	lat, lng := 37.7749, -122.4194
	samples := []ChargingSample{
		chargingSample(0, bptr(false), nil, nil, f64ptr(40), nil, nil),
		chargingSample(30, bptr(true), f64ptr(10.0), f64ptr(7.1), f64ptr(41), &lat, &lng),
		chargingSample(60, bptr(true), f64ptr(15.5), f64ptr(7.0), f64ptr(48), &lat, &lng),
		chargingSample(90, bptr(true), f64ptr(20.0), f64ptr(6.8), f64ptr(55), &lat, &lng),
		chargingSample(120, bptr(false), nil, nil, f64ptr(55), nil, nil),
	}
	sessions := detectChargingSessions(samples)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	s := sessions[0]
	if s.numSamples != 3 {
		t.Fatalf("numSamples = %d, want 3", s.numSamples)
	}
	if got := s.addedEnergyKwh(); got == nil || *got != 10.0 {
		t.Fatalf("addedEnergyKwh = %v, want 10.0", got)
	}
	if got := s.avgPowerKw(); got == nil || *got != 6.96666666666666589691 {
		t.Fatalf("avgPowerKw = %v, want ~6.97", got)
	}
	if s.socStart == nil || *s.socStart != 41 || s.socEnd == nil || *s.socEnd != 55 {
		t.Fatalf("soc start/end = %v/%v, want 41/55", s.socStart, s.socEnd)
	}
	if s.lat == nil || *s.lat != lat {
		t.Fatalf("lat = %v, want %v", s.lat, lat)
	}
}

func TestDetectChargingSessions_TooFewSamplesDiscarded(t *testing.T) {
	samples := []ChargingSample{
		chargingSample(0, bptr(false), nil, nil, nil, nil, nil),
		chargingSample(30, bptr(true), f64ptr(5.0), f64ptr(7.0), nil, nil, nil),
		chargingSample(60, bptr(false), nil, nil, nil, nil, nil),
	}
	if got := detectChargingSessions(samples); len(got) != 0 {
		t.Fatalf("got %d sessions, want 0 (single-sample session discarded)", len(got))
	}
}

func TestDetectChargingSessions_CableConnectedNotChargingOpensNoSession(t *testing.T) {
	// IsCharging false throughout — a connected-but-not-charging vehicle
	// (e.g. finished charging, or on a schedule delay) must not open a
	// session even though it may be plugged in.
	samples := []ChargingSample{
		chargingSample(0, bptr(false), nil, nil, nil, nil, nil),
		chargingSample(30, bptr(false), nil, nil, nil, nil, nil),
		chargingSample(60, bptr(false), nil, nil, nil, nil, nil),
	}
	if got := detectChargingSessions(samples); len(got) != 0 {
		t.Fatalf("got %d sessions, want 0", len(got))
	}
}

func TestDetectChargingSessions_ExplicitFalseSplitsSession(t *testing.T) {
	// isCharging reports an EXPLICIT false for one sample mid-session (not a
	// missing/nil report) — that ends the current run. Two 2-sample
	// sessions, not one. Contrast with GapBetweenTrueSamplesDoesNotSplit,
	// where the middle sample is nil (no report) rather than false.
	samples := []ChargingSample{
		chargingSample(0, bptr(true), f64ptr(1.0), f64ptr(7.0), nil, nil, nil),
		chargingSample(30, bptr(true), f64ptr(2.0), f64ptr(7.0), nil, nil, nil),
		chargingSample(60, bptr(false), nil, nil, nil, nil, nil),
		chargingSample(90, bptr(true), f64ptr(3.0), f64ptr(7.0), nil, nil, nil),
		chargingSample(120, bptr(true), f64ptr(4.0), f64ptr(7.0), nil, nil, nil),
	}
	sessions := detectChargingSessions(samples)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
}

func TestDetectChargingSessions_EnergyCounterResetYieldsNilEnergy(t *testing.T) {
	// AddedEnergy counter resets mid-session (e.g. a new charge cycle
	// re-zeroed it) — last-minus-first goes negative. Same guard as
	// GeofenceDetectionService.engineRuntimeS: report nil, never a negative.
	// Spans >=5 minutes so it isn't itself discarded by the noise filter
	// (nil energy + under minChargingSessionDuration) this test isn't about.
	samples := []ChargingSample{
		chargingSample(0, bptr(true), f64ptr(18.0), nil, nil, nil, nil),
		chargingSample(30, bptr(true), f64ptr(2.0), nil, nil, nil, nil),
		chargingSample(360, bptr(true), f64ptr(4.0), nil, nil, nil, nil),
	}
	sessions := detectChargingSessions(samples)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if got := sessions[0].addedEnergyKwh(); got != nil {
		t.Fatalf("addedEnergyKwh = %v, want nil", got)
	}
}

func TestDetectChargingSessions_NilIsChargingNeverOpensSession(t *testing.T) {
	// A vehicle that never reports the signal at all (nil throughout) must
	// never open a session — nil is "no information", not "charging".
	samples := []ChargingSample{
		chargingSample(0, nil, nil, nil, nil, nil, nil),
		chargingSample(30, nil, nil, nil, nil, nil, nil),
	}
	if got := detectChargingSessions(samples); len(got) != 0 {
		t.Fatalf("got %d sessions, want 0", len(got))
	}
}

func TestDetectChargingSessions_GapBetweenTrueSamplesDoesNotSplit(t *testing.T) {
	// telemetry-api's interval buckets are sparse, not forward-filled: a
	// connection that only phones home once a day reports nil for nearly
	// every bucket even while actively charging. A missing report must not
	// end a session — only an explicit false does (see
	// ExplicitFalseSplitsSession). One session spanning the gap, counting
	// only the two actual true samples.
	samples := []ChargingSample{
		chargingSample(0, bptr(true), f64ptr(1.0), f64ptr(7.0), f64ptr(40), nil, nil),
		chargingSample(30, nil, nil, nil, nil, nil, nil),
		chargingSample(60, nil, nil, nil, nil, nil, nil),
		chargingSample(90, nil, nil, nil, nil, nil, nil),
		chargingSample(120, bptr(true), f64ptr(9.0), f64ptr(7.0), f64ptr(55), nil, nil),
	}
	sessions := detectChargingSessions(samples)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	s := sessions[0]
	if s.numSamples != 2 {
		t.Fatalf("numSamples = %d, want 2 (only the two true samples counted, not the nil gaps)", s.numSamples)
	}
	if !s.endedAt.Equal(chargingSample(120, nil, nil, nil, nil, nil, nil).Time) {
		t.Fatalf("endedAt = %v, want the last true sample's time", s.endedAt)
	}
	if got := s.addedEnergyKwh(); got == nil || *got != 8.0 {
		t.Fatalf("addedEnergyKwh = %v, want 8.0", got)
	}
}

func TestDetectChargingSessions_ZeroEnergyShortRunDiscardedAsNoise(t *testing.T) {
	// Two consecutive true samples (passes the numSamples>=2 rule), but zero
	// energy transferred and under 5 minutes — a connector self-check or
	// relay blip, not a real charging session. Observed on a real vehicle:
	// dozens of exactly-60-second, zero-kWh, unchanged-SOC "sessions".
	samples := []ChargingSample{
		chargingSample(0, bptr(true), f64ptr(50.0), nil, f64ptr(73.0), nil, nil),
		chargingSample(30, bptr(true), f64ptr(50.0), nil, f64ptr(73.0), nil, nil),
	}
	if got := detectChargingSessions(samples); len(got) != 0 {
		t.Fatalf("got %d sessions, want 0 (zero-energy, sub-threshold-duration run is noise)", len(got))
	}
}

func TestDetectChargingSessions_LongZeroEnergyRunKept(t *testing.T) {
	// A run spanning >=5 minutes is kept even with an unmeasurable energy
	// delta (e.g. a long, very low-power trickle the counter's resolution
	// can't register) — duration alone is enough evidence this wasn't a
	// momentary blip.
	samples := []ChargingSample{
		chargingSample(0, bptr(true), f64ptr(50.0), nil, nil, nil, nil),
		chargingSample(30, bptr(true), f64ptr(50.0), nil, nil, nil, nil),
		chargingSample(330, bptr(true), f64ptr(50.0), nil, nil, nil, nil),
	}
	if got := detectChargingSessions(samples); len(got) != 1 {
		t.Fatalf("got %d sessions, want 1 (long enough to count despite zero measured energy)", len(got))
	}
}
