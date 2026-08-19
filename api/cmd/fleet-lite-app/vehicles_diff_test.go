package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The TRAST case, 2026-08-19: nine entitled vehicles, one stale local row for a
// token whose entitlement was revoked the day before, and no overlap at all.
// This is the shape the command exists to print — before the fix it printed
// nothing, because nothing ran.
func TestCompareVehicleSetsIncidentShape(t *testing.T) {
	entitled := []int64{101, 102, 103, 104, 105, 106, 107, 108, 109}
	local := []int64{190171}

	d := compareVehicleSets(entitled, local)

	assert.Empty(t, d.Agree)
	assert.Len(t, d.MissingLocal, 9, "every entitled vehicle is missing locally")
	assert.Equal(t, []int64{190171}, d.ExtraLocal, "the revoked row was never pruned")
}

func TestCompareVehicleSetsBuckets(t *testing.T) {
	// 3 is shared; 1 and 2 are entitled only; 9 is local only.
	d := compareVehicleSets([]int64{3, 1, 2}, []int64{9, 3})

	assert.Equal(t, []int64{3}, d.Agree)
	assert.Equal(t, []int64{1, 2}, d.MissingLocal, "buckets are sorted, not insertion-ordered")
	assert.Equal(t, []int64{9}, d.ExtraLocal)
}

// An agreeing tenant must produce a clean run — this is what makes a non-zero
// exit mean something.
func TestCompareVehicleSetsAgreeing(t *testing.T) {
	d := compareVehicleSets([]int64{5, 4, 6}, []int64{6, 5, 4})

	assert.Equal(t, []int64{4, 5, 6}, d.Agree, "order is not a difference")
	assert.Empty(t, d.MissingLocal)
	assert.Empty(t, d.ExtraLocal)
}

// A tenant entitled to nothing with no local rows is the legitimate empty case
// and must not be reported as a discrepancy. Note the caller — not this
// function — is responsible for having confirmed explicit mode first: for an
// implicit-mode tenant the entitlement endpoint also answers empty, and every
// local row would land in ExtraLocal.
func TestCompareVehicleSetsBothEmpty(t *testing.T) {
	d := compareVehicleSets(nil, nil)

	assert.Empty(t, d.Agree)
	assert.Empty(t, d.MissingLocal)
	assert.Empty(t, d.ExtraLocal)
}

// Duplicate ids on either side — a defensive case, since a token id is unique
// per tenant on both sides — must not inflate the counts a human reads.
func TestCompareVehicleSetsDeduplicates(t *testing.T) {
	d := compareVehicleSets([]int64{7, 7, 8}, []int64{7, 7})

	assert.Equal(t, []int64{7}, d.Agree)
	assert.Equal(t, []int64{8}, d.MissingLocal)
	assert.Empty(t, d.ExtraLocal)
}
