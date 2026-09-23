// api/internal/service/charging_detection.go
package service

import "time"

// detectedChargingSession is one charging session as reported by
// telemetry-api's native `recharge` segmentation, mapped into the shape
// ChargingDetectionService persists. telemetry-api owns run detection
// (including tolerance for reporting gaps and filtering of connector
// self-check / relay-click noise) — this package no longer re-derives it
// from raw samples.
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
