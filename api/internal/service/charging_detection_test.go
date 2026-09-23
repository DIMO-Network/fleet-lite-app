// api/internal/service/charging_detection_test.go
package service

import "testing"

func segment(startTS, endTS string, lat, lng float64, energyFirst, energyLast, avgPower, socFirst, socLast *float64) Segment {
	seg := Segment{
		Start: SegmentPoint{Timestamp: startTS},
		End:   SegmentPoint{Timestamp: endTS},
	}
	seg.Start.Value.Latitude = lat
	seg.Start.Value.Longitude = lng
	add := func(name, agg string, v *float64) {
		if v == nil {
			return
		}
		seg.Signals = append(seg.Signals, SegmentSignal{Name: name, Agg: agg, Value: *v})
	}
	add("powertrainTractionBatteryChargingAddedEnergy", "FIRST", energyFirst)
	add("powertrainTractionBatteryChargingAddedEnergy", "LAST", energyLast)
	add("powertrainTractionBatteryChargingPower", "AVG", avgPower)
	add("powertrainTractionBatteryStateOfChargeCurrent", "FIRST", socFirst)
	add("powertrainTractionBatteryStateOfChargeCurrent", "LAST", socLast)
	return seg
}

func f64ptr(f float64) *float64 { return &f }

func TestSessionFromSegment_MapsFieldsFromSignals(t *testing.T) {
	seg := segment(
		"2026-09-01T08:00:00Z", "2026-09-01T08:30:00Z",
		37.7749, -122.4194,
		f64ptr(10.0), f64ptr(20.0), f64ptr(6.97), f64ptr(41), f64ptr(55),
	)
	s := sessionFromSegment(seg)

	if got := s.addedEnergyKwh(); got == nil || *got != 10.0 {
		t.Fatalf("addedEnergyKwh = %v, want 10.0", got)
	}
	if s.avgPowerKw == nil || *s.avgPowerKw != 6.97 {
		t.Fatalf("avgPowerKw = %v, want 6.97", s.avgPowerKw)
	}
	if s.socStart == nil || *s.socStart != 41 || s.socEnd == nil || *s.socEnd != 55 {
		t.Fatalf("soc start/end = %v/%v, want 41/55", s.socStart, s.socEnd)
	}
	if s.lat == nil || *s.lat != 37.7749 || s.lng == nil || *s.lng != -122.4194 {
		t.Fatalf("lat/lng = %v/%v, want 37.7749/-122.4194", s.lat, s.lng)
	}
	if s.startedAt.IsZero() || s.endedAt.IsZero() {
		t.Fatalf("startedAt/endedAt not parsed: %v / %v", s.startedAt, s.endedAt)
	}
}

func TestSessionFromSegment_MissingSignalsYieldNilFields(t *testing.T) {
	seg := segment("2026-09-01T08:00:00Z", "2026-09-01T08:05:00Z", 0, 0, nil, nil, nil, nil, nil)
	s := sessionFromSegment(seg)

	if got := s.addedEnergyKwh(); got != nil {
		t.Fatalf("addedEnergyKwh = %v, want nil", got)
	}
	if s.avgPowerKw != nil {
		t.Fatalf("avgPowerKw = %v, want nil", s.avgPowerKw)
	}
	if s.socStart != nil || s.socEnd != nil {
		t.Fatalf("soc start/end = %v/%v, want nil/nil", s.socStart, s.socEnd)
	}
	if s.lat != nil || s.lng != nil {
		t.Fatalf("lat/lng = %v/%v, want nil/nil (no location reported)", s.lat, s.lng)
	}
}

func TestAddedEnergyKwh_NegativeDeltaYieldsNil(t *testing.T) {
	// AddedEnergy counter resets mid-session (e.g. a new charge cycle
	// re-zeroed it) — last-minus-first goes negative. Same guard as
	// GeofenceDetectionService.engineRuntimeS: report nil, never a negative.
	seg := segment("2026-09-01T08:00:00Z", "2026-09-01T08:06:00Z", 0, 0, f64ptr(18.0), f64ptr(4.0), nil, nil, nil)
	s := sessionFromSegment(seg)
	if got := s.addedEnergyKwh(); got != nil {
		t.Fatalf("addedEnergyKwh = %v, want nil", got)
	}
}
