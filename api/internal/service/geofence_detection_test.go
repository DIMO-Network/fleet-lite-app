package service

import (
	"testing"
	"time"
)

// a unit square geofence: lon/lat in [0,1], as a closed GeoJSON ring ([lon,lat]).
var squareRings = [][][]float64{{
	{0, 0}, {1, 0}, {1, 1}, {0, 1}, {0, 0},
}}

// a square with a hole in the middle ([0.4,0.6] box).
var squareWithHole = [][][]float64{
	{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {0, 0}},
	{{0.4, 0.4}, {0.6, 0.4}, {0.6, 0.6}, {0.4, 0.6}, {0.4, 0.4}},
}

func TestPointInPolygon(t *testing.T) {
	cases := []struct {
		name     string
		lat, lng float64
		rings    [][][]float64
		want     bool
	}{
		{"center inside", 0.5, 0.5, squareRings, true},
		{"clearly outside", 2, 2, squareRings, false},
		{"negative outside", -0.1, 0.5, squareRings, false},
		{"inside but in hole", 0.5, 0.5, squareWithHole, false},
		{"inside ring outside hole", 0.2, 0.2, squareWithHole, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pointInPolygon(tc.lat, tc.lng, tc.rings); got != tc.want {
				t.Fatalf("pointInPolygon(%v,%v) = %v, want %v", tc.lat, tc.lng, got, tc.want)
			}
		})
	}
}

func sample(sec int, lat, lng float64, speed *float64) GeoSample {
	return GeoSample{
		Time:     time.Date(2026, 6, 25, 12, 0, sec, 0, time.UTC),
		Lat:      lat,
		Lng:      lng,
		SpeedKph: speed,
	}
}

func fptr(f float64) *float64 { return &f }

func TestDetectPasses_SinglePass(t *testing.T) {
	// out, out, IN, IN, IN, out — one pass over samples at 30,60,90s.
	samples := []GeoSample{
		sample(0, 5, 5, nil),            // outside
		sample(30, 2, 2, nil),           // outside
		sample(60, 0.5, 0.5, fptr(40)),  // inside
		sample(90, 0.6, 0.5, fptr(70)),  // inside, fastest
		sample(120, 0.7, 0.6, fptr(55)), // inside
		sample(150, 9, 9, nil),          // outside
	}
	passes := detectPasses(samples, squareRings)
	if len(passes) != 1 {
		t.Fatalf("want 1 pass, got %d", len(passes))
	}
	p := passes[0]
	if p.numSamples != 3 {
		t.Errorf("numSamples = %d, want 3", p.numSamples)
	}
	if got := int(p.exitedAt.Sub(p.enteredAt).Seconds()); got != 60 {
		t.Errorf("dwell = %ds, want 60", got)
	}
	if p.maxSpeed == nil || *p.maxSpeed != 70 {
		t.Errorf("maxSpeed = %v, want 70", p.maxSpeed)
	}
	if p.maxSpeedLat == nil || *p.maxSpeedLat != 0.6 {
		t.Errorf("maxSpeedLat = %v, want 0.6", p.maxSpeedLat)
	}
	if p.entryLat != 0.5 || p.exitLng != 0.6 {
		t.Errorf("entry/exit coords wrong: %+v", p)
	}
}

func TestDetectPasses_MultiplePassesAndOngoing(t *testing.T) {
	// IN, out, IN, IN(end) — two passes; the second is still inside at the end.
	samples := []GeoSample{
		sample(0, 0.5, 0.5, nil),  // pass 1 (single sample)
		sample(30, 8, 8, nil),     // outside
		sample(60, 0.2, 0.2, nil), // pass 2 start
		sample(90, 0.3, 0.3, nil), // pass 2 still inside at end
	}
	passes := detectPasses(samples, squareRings)
	if len(passes) != 2 {
		t.Fatalf("want 2 passes, got %d", len(passes))
	}
	if passes[0].numSamples != 1 {
		t.Errorf("pass 1 numSamples = %d, want 1", passes[0].numSamples)
	}
	if passes[1].numSamples != 2 {
		t.Errorf("pass 2 numSamples = %d, want 2", passes[1].numSamples)
	}
}

func TestDetectPasses_NeverInside(t *testing.T) {
	samples := []GeoSample{
		sample(0, 5, 5, nil),
		sample(30, 6, 6, nil),
	}
	if passes := detectPasses(samples, squareRings); len(passes) != 0 {
		t.Fatalf("want 0 passes, got %d", len(passes))
	}
}
