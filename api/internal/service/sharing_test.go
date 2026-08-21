package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

const (
	signableOwner   = "0x1111111111111111111111111111111111111111"
	unsignableOwner = "0x2222222222222222222222222222222222222222"
)

var sharingTenant = models.Tenant{ID: "7be1ab9e-0000-0000-0000-000000000001", ClientID: "0xabc"}

// fakeResolver stands in for the tenancy client, recording what it was asked.
type fakeResolver struct {
	shareable []string
	err       error
	calls     int
	asked     []string
}

func (f *fakeResolver) Configured() bool { return true }
func (f *fakeResolver) ShareableOwners(_ context.Context, _ models.Tenant, owners []string) ([]string, error) {
	f.calls++
	f.asked = append([]string{}, owners...)
	if f.err != nil {
		return nil, f.err
	}
	return f.shareable, nil
}

// sharingFixture wires a SharingService at a resolver that reports only
// signableOwner as shareable.
func sharingFixture(t *testing.T) (*SharingService, *fakeResolver) {
	t.Helper()
	logger := zerolog.Nop()
	res := &fakeResolver{shareable: []string{signableOwner}}
	return NewSharingService(&logger, res), res
}

func TestAnnotateCanShare(t *testing.T) {
	svc, _ := sharingFixture(t)
	vehicles := []models.Vehicle{
		{TokenID: 1, Owner: signableOwner},
		{TokenID: 2, Owner: unsignableOwner},
	}

	svc.AnnotateCanShare(context.Background(), sharingTenant, vehicles)

	assert.True(t, vehicles[0].CanShare)
	assert.Empty(t, vehicles[0].ShareBlocker, "shareable carries no blocker")
	assert.False(t, vehicles[1].CanShare, "an owner the tenant cannot sign for gets no share button")
	assert.Equal(t, models.ShareBlockerOwner, vehicles[1].ShareBlocker,
		"and the UI is told WHY, so it can explain instead of hiding")
}

// Owner addresses reach this app from identity-api and from its own database.
// A casing difference must not hide the button — that failure would look like
// the feature being off for one customer and working for another.
func TestAnnotateCanShare_IsCaseInsensitive(t *testing.T) {
	svc, _ := sharingFixture(t)
	vehicles := []models.Vehicle{{TokenID: 1, Owner: "0x1111111111111111111111111111111111111111"}}

	svc.AnnotateCanShare(context.Background(), sharingTenant, vehicles)
	assert.True(t, vehicles[0].CanShare)
}

// A customer tenant's fleet usually sits on one kernel account, so the same
// owner arrives once per vehicle. One upstream call, not one per vehicle.
func TestAnnotateCanShare_DeduplicatesOwners(t *testing.T) {
	svc, res := sharingFixture(t)

	vehicles := make([]models.Vehicle, 40)
	for i := range vehicles {
		vehicles[i] = models.Vehicle{TokenID: int64(i), Owner: signableOwner}
	}
	svc.AnnotateCanShare(context.Background(), sharingTenant, vehicles)

	assert.Equal(t, 1, res.calls, "forty vehicles on one owner is one question")
	assert.Len(t, res.asked, 1)
	for i := range vehicles {
		assert.True(t, vehicles[i].CanShare)
	}
}

// THE TRADE THIS SERVICE MAKES. canShare is a display gate on the hot path, so
// an upstream failure hides the button and returns the fleet. Failing GET
// /vehicles instead would blank the customer's whole list to conceal one
// button — and the share endpoint fails loudly anyway, which is where a real
// problem needs to surface.
func TestAnnotateCanShare_UpstreamFailureHidesButtonsWithoutFailing(t *testing.T) {
	logger := zerolog.Nop()
	svc := NewSharingService(&logger, &fakeResolver{err: errors.New("tenancy unavailable")})
	vehicles := []models.Vehicle{{TokenID: 1, Owner: signableOwner}}

	svc.AnnotateCanShare(context.Background(), sharingTenant, vehicles)

	assert.False(t, vehicles[0].CanShare, "unknown means no enabled button, never an assumed yes")
	assert.Equal(t, models.ShareBlockerUnknown, vehicles[0].ShareBlocker,
		"a failed lookup reports UNKNOWN — claiming the owner refused when we never asked would send someone chasing an authorization problem that is an outage")
}

// A vehicle whose owner is not an address (unsynced, or a malformed row) must
// be skipped rather than sent upstream as a candidate.
func TestAnnotateCanShare_SkipsMalformedOwners(t *testing.T) {
	svc, res := sharingFixture(t)
	vehicles := []models.Vehicle{
		{TokenID: 1, Owner: ""},
		{TokenID: 2, Owner: "not-an-address"},
		{TokenID: 3, Owner: signableOwner},
	}

	svc.AnnotateCanShare(context.Background(), sharingTenant, vehicles)

	assert.Equal(t, 1, res.calls)
	assert.Len(t, res.asked, 1, "only the real address is asked about")
	assert.False(t, vehicles[0].CanShare)
	assert.Equal(t, models.ShareBlockerNoOwner, vehicles[0].ShareBlocker)
	assert.False(t, vehicles[1].CanShare)
	assert.Equal(t, models.ShareBlockerNoOwner, vehicles[1].ShareBlocker)
	assert.True(t, vehicles[2].CanShare)
	assert.Empty(t, vehicles[2].ShareBlocker)
}

// With no vehicles there is nothing to ask, and with no tenancy client there is
// no signer and no share path at all.
func TestAnnotateCanShare_NoWorkMeansNoCall(t *testing.T) {
	svc, res := sharingFixture(t)
	svc.AnnotateCanShare(context.Background(), sharingTenant, nil)
	assert.Zero(t, res.calls)

	logger := zerolog.Nop()
	unconfigured := NewSharingService(&logger, nil)
	assert.False(t, unconfigured.Configured())
	unconfigured.AnnotateCanShare(context.Background(), sharingTenant,
		[]models.Vehicle{{TokenID: 1, Owner: signableOwner}})
}
