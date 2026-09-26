// api/internal/service/charging_detection_test.go
package service

import (
	"testing"
	"time"
)

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

func TestIsNoise_ZeroEnergyShortRunDiscarded(t *testing.T) {
	// Observed directly in production: telemetry-api's native `recharge`
	// segmentation reports exactly-60-second, zero-kWh runs (connector
	// self-checks / relay clicks) as sessions in their own right — it does
	// not filter these itself.
	seg := segment("2026-09-01T08:00:00Z", "2026-09-01T08:01:00Z", 0, 0, f64ptr(50.0), f64ptr(50.0), nil, nil, nil)
	if s := sessionFromSegment(seg); !s.isNoise() {
		t.Fatalf("isNoise() = false, want true (zero-energy, sub-threshold-duration run)")
	}
}

func TestIsNoise_LongZeroEnergyRunKept(t *testing.T) {
	// A run spanning >=5 minutes is kept even with an unmeasurable energy
	// delta (e.g. a long, very low-power trickle the counter's resolution
	// can't register) — duration alone is enough evidence this wasn't a
	// momentary blip.
	seg := segment("2026-09-01T08:00:00Z", "2026-09-01T08:05:00Z", 0, 0, f64ptr(50.0), f64ptr(50.0), nil, nil, nil)
	if s := sessionFromSegment(seg); s.isNoise() {
		t.Fatalf("isNoise() = true, want false (long enough to count despite zero measured energy)")
	}
}

func TestIsNoise_MeasurableEnergyKeptEvenIfShort(t *testing.T) {
	// A real (if brief) top-up still adds some energy, so measurable energy
	// alone is enough to keep a sub-threshold-duration run.
	seg := segment("2026-09-01T08:00:00Z", "2026-09-01T08:00:30Z", 0, 0, f64ptr(50.0), f64ptr(50.06), nil, nil, nil)
	if s := sessionFromSegment(seg); s.isNoise() {
		t.Fatalf("isNoise() = true, want false (measurable energy transferred)")
	}
}

// session builds a detected session from RFC3339 times, the way telemetry-api
// reports recharge: always an end, never isOngoing.
func session(t *testing.T, start, end string) detectedChargingSession {
	t.Helper()
	return sessionFromSegment(segment(start, end, 0, 0, nil, nil, nil, nil, nil))
}

func at(t *testing.T, ts string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSettleGap_HistoricalGapIsFinalAsAWhole(t *testing.T) {
	now := at(t, "2026-09-25T12:00:00Z")
	gap := timeInterval{at(t, "2026-09-01T00:00:00Z"), at(t, "2026-09-20T00:00:00Z")}
	// Ends exactly at the gap's end (clipped by a covered interval after it):
	// still final, because nothing before the cutoff can change.
	s := session(t, "2026-09-19T22:00:00Z", "2026-09-20T00:00:00Z")
	final, inProgress, coveredTo := settleGap([]detectedChargingSession{s}, gap, now)
	if len(final) != 1 || len(inProgress) != 0 || !coveredTo.Equal(gap.to) {
		t.Fatalf("final=%d inProgress=%d coveredTo=%v, want 1, 0, %v", len(final), len(inProgress), coveredTo, gap.to)
	}
}

// The bug #181 meant to fix: telemetry-api reports a charge still under way
// with isOngoing=false and its latest reading as its end.
func TestSettleGap_ChargeStillRisingIsNotFinal(t *testing.T) {
	now := at(t, "2026-09-25T12:00:00Z")
	gap := timeInterval{at(t, "2026-09-25T00:00:00Z"), now}
	done := session(t, "2026-09-25T02:00:00Z", "2026-09-25T03:00:00Z")
	charging := session(t, "2026-09-25T11:00:00Z", "2026-09-25T11:58:00Z")

	final, inProgress, coveredTo := settleGap([]detectedChargingSession{done, charging}, gap, now)
	if len(final) != 1 || !final[0].startedAt.Equal(done.startedAt) {
		t.Fatalf("final = %+v, want only the 02:00 session", final)
	}
	if len(inProgress) != 1 || !inProgress[0].startedAt.Equal(charging.startedAt) {
		t.Fatalf("inProgress = %+v, want the 11:00 session", inProgress)
	}
	// The live session starts after the cutoff, so coverage stops at the
	// cutoff: the next request re-reads from there and finds it again.
	if want := now.Add(-rechargeSettleMargin); !coveredTo.Equal(want) {
		t.Fatalf("coveredTo = %v, want the cutoff %v", coveredTo, want)
	}
}

// A long charge that began before the cutoff holds coverage at its start, so
// the whole session is re-read, and stored once, when it finishes.
func TestSettleGap_LongChargeHoldsCoverageAtItsStart(t *testing.T) {
	now := at(t, "2026-09-25T12:00:00Z")
	gap := timeInterval{at(t, "2026-09-25T00:00:00Z"), now}
	long := session(t, "2026-09-25T08:00:00Z", "2026-09-25T11:59:00Z")
	final, inProgress, coveredTo := settleGap([]detectedChargingSession{long}, gap, now)
	if len(final) != 0 || len(inProgress) != 1 || !coveredTo.Equal(long.startedAt) {
		t.Fatalf("final=%d inProgress=%d coveredTo=%v, want 0, 1, %v", len(final), len(inProgress), coveredTo, long.startedAt)
	}
}

// telemetry-api merges rises up to two hours apart, so a session that ended an
// hour ago can still be extended by the next top-up.
func TestSettleGap_RecentlyEndedSessionCanStillBeExtended(t *testing.T) {
	now := at(t, "2026-09-25T12:00:00Z")
	gap := timeInterval{at(t, "2026-09-25T00:00:00Z"), now}
	recent := session(t, "2026-09-25T09:30:00Z", "2026-09-25T11:00:00Z")
	final, inProgress, coveredTo := settleGap([]detectedChargingSession{recent}, gap, now)
	if len(final) != 0 || len(inProgress) != 1 || !coveredTo.Equal(recent.startedAt) {
		t.Fatalf("final=%d inProgress=%d coveredTo=%v, want 0, 1, %v", len(final), len(inProgress), coveredTo, recent.startedAt)
	}
}

func TestSettleGap_NothingInProgressStillLeavesAMarginForLateReadings(t *testing.T) {
	now := at(t, "2026-09-25T12:00:00Z")
	gap := timeInterval{at(t, "2026-09-25T00:00:00Z"), now}
	_, _, coveredTo := settleGap(nil, gap, now)
	if want := now.Add(-rechargeSettleMargin); !coveredTo.Equal(want) {
		t.Fatalf("coveredTo = %v, want %v", coveredTo, want)
	}
}

func TestSettleGap_OngoingWithoutAnEndIsInProgress(t *testing.T) {
	now := at(t, "2026-09-25T12:00:00Z")
	gap := timeInterval{at(t, "2026-09-25T00:00:00Z"), now}
	open := sessionFromSegment(segment("2026-09-25T01:00:00Z", "", 0, 0, nil, nil, nil, nil, nil))
	open.ongoing = true
	final, inProgress, coveredTo := settleGap([]detectedChargingSession{open}, gap, now)
	if len(final) != 0 || len(inProgress) != 1 || !coveredTo.Equal(open.startedAt) {
		t.Fatalf("final=%d inProgress=%d coveredTo=%v", len(final), len(inProgress), coveredTo)
	}
}

func TestSettleGap_GapInsideTheMarginRecordsNothing(t *testing.T) {
	now := at(t, "2026-09-25T12:00:00Z")
	gap := timeInterval{at(t, "2026-09-25T11:00:00Z"), now}
	final, _, coveredTo := settleGap(nil, gap, now)
	if len(final) != 0 || !coveredTo.Equal(gap.from) {
		t.Fatalf("final=%d coveredTo=%v, want 0 and the gap start (nothing to record)", len(final), coveredTo)
	}
}
