// api/internal/service/charging_detection.go
package service

import "time"

// minChargingSessionDuration mirrors the pre-native-segments noise filter.
// telemetry-api's `recharge` segmentation finds rises in state of charge
// while the vehicle is stationary (merging rises up to two hours apart), but
// does NOT itself filter connector self-check / relay-click blips — observed
// directly in production (2026-09-24, vehicle 189017): dozens of
// exactly-60-second, zero-kWh, unchanged-SOC "sessions" alongside genuine
// ones, once native segments replaced the old sweep+filter. A session is kept
// only if it either transferred measurable energy or lasted at least this long.
const minChargingSessionDuration = 5 * time.Minute

// rechargeSettleMargin is how long after its last reading a recharge session
// can still change, and so how recent a session must be to count as still in
// progress.
//
// telemetry-api never marks a recharge session ongoing: a charge still under
// way comes back with isOngoing=false and its latest reading as its end
// (rechargeSessionsToSegments in its recharge detector). It also folds rises
// separated by up to two hours into one session (rechargeSessionGapMax), so a
// session that ended an hour ago can still be extended. Two hours, plus slack
// for readings that arrive late.
const rechargeSettleMargin = 2*time.Hour + 15*time.Minute

// detectedChargingSession is one charging session as reported by
// telemetry-api's native `recharge` segmentation, mapped into the shape
// ChargingDetectionService persists. telemetry-api owns run detection
// (gap tolerance for sparse reporting) but not noise filtering — see
// minChargingSessionDuration and isNoise.
type detectedChargingSession struct {
	startedAt time.Time
	endedAt   time.Time
	// ongoing is telemetry-api's isOngoing. Never set for recharge today (see
	// rechargeSettleMargin), honoured in case it ever is.
	ongoing        bool
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
		ongoing:        seg.IsOngoing,
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

// settleGap decides, for one scanned gap, which sessions are final and how far
// the gap can be recorded as scanned.
//
// A session whose end is more than rechargeSettleMargin before now is final.
// One that ends later, has no end, or is marked ongoing may still grow: it is
// returned in inProgress, is never stored, and coverage stops at its start so
// the next request re-reads it from there. With nothing in progress, coverage
// still stops at the cutoff, so readings that arrive late are read again. A gap
// that ends before the cutoff is final as a whole; nothing in it can change.
//
// Sessions never overlap, so every session starting before coveredTo ends
// before it: final is exactly the sessions that start before coveredTo.
func settleGap(sessions []detectedChargingSession, gap timeInterval, now time.Time) (final, inProgress []detectedChargingSession, coveredTo time.Time) {
	cutoff := now.Add(-rechargeSettleMargin)
	if !gap.to.After(cutoff) {
		return sessions, nil, gap.to
	}
	coveredTo = cutoff
	for _, s := range sessions {
		if s.ongoing || s.endedAt.IsZero() || s.endedAt.After(cutoff) {
			if s.startedAt.Before(coveredTo) {
				coveredTo = s.startedAt
			}
		}
	}
	if coveredTo.Before(gap.from) {
		coveredTo = gap.from
	}
	for _, s := range sessions {
		if s.startedAt.Before(coveredTo) {
			final = append(final, s)
		} else {
			inProgress = append(inProgress, s)
		}
	}
	return final, inProgress, coveredTo
}
