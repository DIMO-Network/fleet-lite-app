package service

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Payloads in testdata/ were captured 2026-09-10 from telemetry-api with the
// app's dev license: token 186612 is a Ruptela F-150 (dense events), 180895 a
// HashDog Camry (its connection never emits events). The dailyActivity window
// was now-3d..now with timezone America/Bogota, which touches four calendar
// days there.
var (
	behaviorFrom = time.Date(2026, 9, 7, 15, 30, 0, 0, time.UTC)
	behaviorTo   = time.Date(2026, 9, 10, 15, 30, 0, 0, time.UTC)
)

func loadBogota(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Bogota")
	require.NoError(t, err)
	return loc
}

func TestBehaviorDates(t *testing.T) {
	bogota := loadBogota(t)
	assert.Equal(t, []string{"2026-09-07", "2026-09-08", "2026-09-09", "2026-09-10"}, behaviorDates(behaviorFrom, behaviorTo, bogota))

	// 02:00 UTC on the 10th is still the 9th in Bogota (UTC-5): the window's
	// day count follows the zone, not UTC.
	late := time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC)
	assert.Equal(t, []string{"2026-09-09"}, behaviorDates(late, late, bogota))
	assert.Equal(t, []string{"2026-09-10"}, behaviorDates(late, late, time.UTC))
}

func TestParseBehaviorResponse_Ruptela(t *testing.T) {
	raw, err := os.ReadFile("testdata/behavior_186612.json")
	require.NoError(t, err)

	got, err := parseBehaviorResponse(zerolog.Nop(), raw, behaviorFrom, behaviorTo, loadBogota(t))
	require.NoError(t, err)

	assert.True(t, got.Supported)
	require.Len(t, got.AllTime, 4)
	assert.Equal(t, EventTotal{Name: "behavior.extremeBraking", Count: 4882, FirstSeen: "2026-03-10T16:04:01Z", LastSeen: "2026-09-09T22:17:20Z"}, got.AllTime[0])

	require.Len(t, got.Days, 4)
	for i, d := range []string{"2026-09-07", "2026-09-08", "2026-09-09", "2026-09-10"} {
		assert.Equal(t, d, got.Days[i].Date)
		// Every canonical name is present even when the API reported zero.
		for _, n := range BehaviorEventNames {
			_, ok := got.Days[i].Counts[n]
			assert.True(t, ok, "day %d missing %s", i, n)
		}
	}

	first := got.Days[0]
	assert.Equal(t, 0, first.TripCount)
	assert.Equal(t, 0, first.DriveSeconds)
	assert.Equal(t, map[string]int{
		"behavior.harshBraking": 0, "behavior.extremeBraking": 2,
		"behavior.harshAcceleration": 2, "behavior.harshCornering": 0,
	}, first.Counts)
	require.NotNil(t, first.DistanceKm)
	assert.InDelta(t, 1, *first.DistanceKm, 1e-9) // odometer 13916 -> 13917

	second := got.Days[1]
	assert.Equal(t, 1, second.TripCount)
	assert.Equal(t, 2400, second.DriveSeconds)
	assert.Equal(t, 9, second.Counts["behavior.extremeBraking"])
	require.NotNil(t, second.DistanceKm)
	assert.InDelta(t, 42, *second.DistanceKm, 1e-9)
}

func TestParseBehaviorResponse_Unsupported(t *testing.T) {
	raw, err := os.ReadFile("testdata/behavior_180895.json")
	require.NoError(t, err)

	got, err := parseBehaviorResponse(zerolog.Nop(), raw, behaviorFrom, behaviorTo, loadBogota(t))
	require.NoError(t, err)

	assert.False(t, got.Supported, "empty eventDataSummary means the connection never emits events")
	assert.Empty(t, got.AllTime)
	require.Len(t, got.Days, 4)
	for _, d := range got.Days {
		assert.Nil(t, d.DistanceKm, "no odometer signal on an idle day -> no distance, not zero")
		assert.Equal(t, 0, d.TripCount)
		for _, n := range BehaviorEventNames {
			assert.Equal(t, 0, d.Counts[n])
		}
	}
}

func TestParseBehaviorResponse_DayCountMismatch(t *testing.T) {
	raw, err := os.ReadFile("testdata/behavior_186612.json")
	require.NoError(t, err)

	// A 30-day window against a 4-record payload must not be silently
	// mis-aligned onto the wrong dates.
	_, err = parseBehaviorResponse(zerolog.Nop(), raw, behaviorTo.AddDate(0, 0, -29), behaviorTo, loadBogota(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "4 records for a 30-day window")
}

func TestParseBehaviorResponse_DistanceNeedsBothEnds(t *testing.T) {
	raw := []byte(`{"data":{"dataSummary":{"eventDataSummary":[{"name":"behavior.harshBraking","numberOfEvents":1,"firstSeen":"","lastSeen":""}]},
		"dailyActivity":[{"segmentCount":1,"duration":10,"eventCounts":[],"signals":[{"name":"powertrainTransmissionTravelledDistance","agg":"LAST","value":100}]}]}}`)
	day := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	got, err := parseBehaviorResponse(zerolog.Nop(), raw, day, day, time.UTC)
	require.NoError(t, err)
	require.Len(t, got.Days, 1)
	assert.Nil(t, got.Days[0].DistanceKm)
}

func TestSegmentsParseEventCounts(t *testing.T) {
	raw, err := os.ReadFile("testdata/segments_186612.json")
	require.NoError(t, err)

	var resp struct {
		Data struct {
			Segments []Segment `json:"segments"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	require.NotEmpty(t, resp.Data.Segments)
	seg := resp.Data.Segments[0]
	assert.Equal(t, "2026-09-08T22:21:00Z", seg.Start.Timestamp)
	assert.Contains(t, seg.EventCounts, EventCount{Name: "behavior.extremeBraking", Count: 9})
	assert.Contains(t, seg.EventCounts, EventCount{Name: "behavior.harshBraking", Count: 0})
}

func TestBehaviorEventRequests(t *testing.T) {
	assert.Equal(t,
		`[{ name: "behavior.harshBraking" } { name: "behavior.extremeBraking" } { name: "behavior.harshAcceleration" } { name: "behavior.harshCornering" }]`,
		behaviorEventRequests())
}
