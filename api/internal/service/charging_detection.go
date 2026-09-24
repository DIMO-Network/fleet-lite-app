// api/internal/service/charging_detection.go
package service

import "time"

// minChargingSessionDuration mirrors the pre-native-segments noise filter:
// telemetry-api's `recharge` segmentation reports gap-tolerant runs of
// IsCharging=true, same as our old sweep did, but does NOT itself filter
// connector self-check / relay-click blips — observed directly in
// production (2026-09-24, vehicle 189017): dozens of exactly-60-second,
// zero-kWh, unchanged-SOC "sessions" alongside genuine ones, once native
// segments replaced the old sweep+filter. A session is kept only if it
// either transferred measurable energy or lasted at least this long.
const minChargingSessionDuration = 5 * time.Minute

// detectedChargingSession is one charging session as reported by
// telemetry-api's native `recharge` segmentation, mapped into the shape
// ChargingDetectionService persists. telemetry-api owns run detection
// (gap tolerance for sparse reporting) but not noise filtering — see
// minChargingSessionDuration and isNoise.
type detectedChargingSession struct {
	startedAt      time.Time
	endedAt        time.Time
	lat            *float64
	lng            *float64
	socStart       *float64
	socEnd         *float64
	firstEnergyKwh *float64
	lastEnergyKwh  *float64
	avgPowerKw     *float64
}

// addedEnergyKwh is the session's last-minus-first AddedEnergyKwh reading.
// Returns nil when either end is missing, or when the delta is negative (the
// counter reset mid-session — e.g. a new charge cycle re-zeroed it) — a
// negative number would be meaningless, mirroring
// GeofenceDetectionService's engineRuntimeS guard.
func (s detectedChargingSession) addedEnergyKwh() *float64 {
	if s.firstEnergyKwh == nil || s.lastEnergyKwh == nil {
		return nil
	}
	d := *s.lastEnergyKwh - *s.firstEnergyKwh
	if d < 0 {
		return nil
	}
	return &d
}

// sessionFromSegment maps one telemetry-api recharge Segment into a
// detectedChargingSession ready for pricing/persistence.
func sessionFromSegment(seg Segment) detectedChargingSession {
	parseTime := func(ts string) time.Time {
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return time.Time{}
		}
		return t
	}
	d := detectedChargingSession{
		startedAt:      parseTime(seg.Start.Timestamp),
		endedAt:        parseTime(seg.End.Timestamp),
		firstEnergyKwh: segmentSignalValue(seg.Signals, "powertrainTractionBatteryChargingAddedEnergy", "FIRST"),
		lastEnergyKwh:  segmentSignalValue(seg.Signals, "powertrainTractionBatteryChargingAddedEnergy", "LAST"),
		avgPowerKw:     segmentSignalValue(seg.Signals, "powertrainTractionBatteryChargingPower", "AVG"),
		socStart:       segmentSignalValue(seg.Signals, "powertrainTractionBatteryStateOfChargeCurrent", "FIRST"),
		socEnd:         segmentSignalValue(seg.Signals, "powertrainTractionBatteryStateOfChargeCurrent", "LAST"),
	}
	if seg.Start.Value.Latitude != 0 || seg.Start.Value.Longitude != 0 {
		lat, lng := seg.Start.Value.Latitude, seg.Start.Value.Longitude
		d.lat, d.lng = &lat, &lng
	}
	return d
}

// isNoise reports whether a detected session is a connector self-check,
// relay click, or brief preconditioning blip rather than something a user
// would recognize as charging: no measurable energy transferred and under
// minChargingSessionDuration. A real (if brief) top-up still adds some
// energy, so a zero-energy, sub-threshold run is noise; a long run is kept
// even with an unmeasurable energy delta (e.g. a very low-power trickle the
// counter's resolution can't register) since duration alone is evidence
// enough it wasn't momentary.
func (s detectedChargingSession) isNoise() bool {
	energy := s.addedEnergyKwh()
	longEnough := s.endedAt.Sub(s.startedAt) >= minChargingSessionDuration
	return (energy == nil || *energy <= 0) && !longEnough
}
