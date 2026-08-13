package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRemoteGroups struct {
	groups []models.RemoteFleetGroup
	err    error
	calls  int
}

func (f *fakeRemoteGroups) VehicleGroups(context.Context, models.Tenant) ([]models.RemoteFleetGroup, error) {
	f.calls++
	return f.groups, f.err
}

func remoteViewFixture(remote *fakeRemoteGroups) *FleetGroupService {
	l := zerolog.Nop()
	s := NewFleetGroupService(&l, nil) // nil store: the remote branch must never touch it
	s.UseTenancyReads(remote)
	return s
}

var viewTenant = models.Tenant{ID: "7be1ab9e-0000-0000-0000-000000000001", Name: "Kaufmann"}

func TestViewMethodsServeFromTenancyWhenFlagged(t *testing.T) {
	remote := &fakeRemoteGroups{groups: []models.RemoteFleetGroup{
		{ID: viewTenant.ID + "_priority", Name: "Priority", Color: "#111111", VehicleCount: 1, TokenIDs: []int64{7}},
		{ID: viewTenant.ID + "_vans", Name: "Vans", Color: "#222222", VehicleCount: 2, TokenIDs: []int64{7, 9}},
	}}
	s := remoteViewFixture(remote)
	ctx := context.Background()

	t.Run("ListGroupsView", func(t *testing.T) {
		got, err := s.ListGroupsView(ctx, viewTenant)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "Priority", got[0].Group.Name)
		assert.Equal(t, 1, got[0].VehicleCount)
		assert.Equal(t, viewTenant.ID, got[0].Group.TenantID)
	})

	t.Run("GetGroupView finds by id and carries the member set", func(t *testing.T) {
		g, members, err := s.GetGroupView(ctx, viewTenant, viewTenant.ID+"_vans")
		require.NoError(t, err)
		assert.Equal(t, "Vans", g.Name)
		assert.Equal(t, []int64{7, 9}, members)
	})

	t.Run("GetGroupView misses as ErrGroupNotFound", func(t *testing.T) {
		_, _, err := s.GetGroupView(ctx, viewTenant, viewTenant.ID+"_gone")
		assert.ErrorIs(t, err, ErrGroupNotFound,
			"the controller maps this to 404 exactly as it does for the local read")
	})

	t.Run("VehicleGroupsMapView inverts groups to per-vehicle refs, name-ordered", func(t *testing.T) {
		m, err := s.VehicleGroupsMapView(ctx, viewTenant)
		require.NoError(t, err)
		require.Len(t, m, 2)
		assert.Equal(t, []GroupRef{
			{ID: viewTenant.ID + "_priority", Name: "Priority", Color: "#111111"},
			{ID: viewTenant.ID + "_vans", Name: "Vans", Color: "#222222"},
		}, m[7])
		assert.Equal(t, []GroupRef{{ID: viewTenant.ID + "_vans", Name: "Vans", Color: "#222222"}}, m[9])
	})
}

// A remote failure surfaces; it must not silently downgrade to the local
// tables — an unverified fallback is how a broken read path stays broken.
func TestViewMethodsSurfaceRemoteFailures(t *testing.T) {
	s := remoteViewFixture(&fakeRemoteGroups{err: errors.New("tenancy api 503")})
	ctx := context.Background()

	_, err := s.ListGroupsView(ctx, viewTenant)
	assert.ErrorContains(t, err, "tenancy api 503")
	_, _, err = s.GetGroupView(ctx, viewTenant, "x")
	assert.ErrorContains(t, err, "tenancy api 503")
	_, err = s.VehicleGroupsMapView(ctx, viewTenant)
	assert.ErrorContains(t, err, "tenancy api 503")
}
