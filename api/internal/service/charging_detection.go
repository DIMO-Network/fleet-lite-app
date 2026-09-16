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
// maximal run of consecutive IsCharging=true samples (nil treated as false —
// a vehicle with no charging-signal support simply never opens a session).
// A run shorter than 2 samples is discarded: a single point can't measure an
// energy delta. Mirrors GeofenceDetectionService.detectPasses; the same
// documented limitation applies — a brief signal dropout mid-session (one
// sample flickering false) splits it into two sessions rather than being
// bridged.
func detectChargingSessions(samples []ChargingSample) []detectedChargingSession {
	var sessions []detectedChargingSession
	var cur *detectedChargingSession
	flush := func() {
		if cur != nil && cur.numSamples >= 2 {
			sessions = append(sessions, *cur)
		}
		cur = nil
	}
	for _, smp := range samples {
		charging := smp.IsCharging != nil && *smp.IsCharging
		if !charging {
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
