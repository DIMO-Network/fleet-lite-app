// api/internal/service/charging_detection.go
package service

import "time"

// detectedChargingSession is the internal result of the isCharging sweep
// before persistence.
type detectedChargingSession struct {
	startedAt      time.Time
	endedAt        time.Time
	lat            *float64
	lng            *float64
	socStart       *float64
	socEnd         *float64
	numSamples     int
	firstEnergyKwh *float64
	lastEnergyKwh  *float64
	powerSumKw     float64
	powerSamples   int
}

// addedEnergyKwh is the session's last-minus-first AddedEnergyKwh reading.
// Returns nil when fewer than one energy reading was seen, or when the delta
// is negative (the counter reset mid-session — e.g. a new charge cycle
// re-zeroed it) — a negative number would be meaningless, mirroring
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

// avgPowerKw averages every PowerKw reading seen during the session. Returns
// nil when the vehicle never reported it.
func (s detectedChargingSession) avgPowerKw() *float64 {
	if s.powerSamples == 0 {
		return nil
	}
	avg := s.powerSumKw / float64(s.powerSamples)
	return &avg
}

// detectChargingSessions sweeps ordered samples and emits one session per
// maximal run of IsCharging=true samples, tolerant of gaps where the vehicle
// simply didn't report (nil). A session ends only on an EXPLICIT false —
// never on a missing sample. This matters because telemetry-api's interval
// buckets are sparse, not forward-filled: a connection that phones home only
// once a day reports nil for nearly every 30s bucket even while actively
// charging, so treating nil the same as false (the original implementation)
// meant a real multi-hour session fragmented into isolated single-true-
// sample runs that the <2-samples rule discarded — no charging session was
// ever detected for any connection that doesn't report continuously.
// A run shorter than 2 (non-nil, true) samples is discarded: a single point
// can't measure an energy delta. A run that IS 2+ samples but transferred no
// measurable energy and lasted under minChargingSessionDuration is also
// discarded — a real (if brief) top-up still adds some energy, so a
// zero-energy, sub-threshold run is noise: a connector self-check, a relay
// click, a brief preconditioning blip. Observed on a real vehicle: dozens of
// exactly-60-second, zero-kWh, unchanged-SOC "sessions" the nil-gap fix
// (which made every run reachable, not just continuously-reported ones) now
// surfaced alongside genuine sessions -- these aren't something a user would
// recognize as "charging."
const minChargingSessionDuration = 5 * time.Minute

func detectChargingSessions(samples []ChargingSample) []detectedChargingSession {
	var sessions []detectedChargingSession
	var cur *detectedChargingSession
	flush := func() {
		if cur == nil || cur.numSamples < 2 {
			cur = nil
			return
		}
		energy := cur.addedEnergyKwh()
		longEnough := cur.endedAt.Sub(cur.startedAt) >= minChargingSessionDuration
		if (energy == nil || *energy <= 0) && !longEnough {
			cur = nil
			return
		}
		sessions = append(sessions, *cur)
		cur = nil
	}
	for _, smp := range samples {
		if smp.IsCharging == nil {
			// No report this bucket — carry any open session forward without
			// counting it as a sample. Only an explicit false ends a session.
			continue
		}
		if !*smp.IsCharging {
			flush()
			continue
		}
		if cur == nil {
			cur = &detectedChargingSession{startedAt: smp.Time}
		}
		cur.endedAt = smp.Time
		cur.numSamples++
		if smp.Lat != nil && smp.Lng != nil {
			lat, lng := *smp.Lat, *smp.Lng
			cur.lat, cur.lng = &lat, &lng
		}
		if smp.SocPct != nil {
			v := *smp.SocPct
			if cur.socStart == nil {
				cur.socStart = &v
			}
			cur.socEnd = &v
		}
		if smp.AddedEnergyKwh != nil {
			v := *smp.AddedEnergyKwh
			if cur.firstEnergyKwh == nil {
				cur.firstEnergyKwh = &v
			}
			cur.lastEnergyKwh = &v
		}
		if smp.PowerKw != nil {
			cur.powerSumKw += *smp.PowerKw
			cur.powerSamples++
		}
	}
	flush()
	return sessions
}
