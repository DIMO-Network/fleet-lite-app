package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/lib/pq"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// indexFixture is one tenant's groups as the tenancy endpoint serves them,
// deliberately NOT in name order and with a duplicate token id, so the index is
// shown to impose ordering rather than inherit it.
func indexFixture() []models.RemoteFleetGroup {
	return []models.RemoteFleetGroup{
		{ID: "t_vans", Name: "Vans", Color: "#222222", TokenIDs: []int64{9, 7, 7}},
		{ID: "t_empty", Name: "Empty", Color: "#333333", TokenIDs: nil},
		{ID: "t_priority", Name: "Priority", Color: "#111111", TokenIDs: []int64{7}},
	}
}

func TestGroupIndexTokenIDsForGroups(t *testing.T) {
	idx := NewGroupIndex(indexFixture())

	tests := []struct {
		name     string
		groupIDs []string
		want     []int64
	}{
		{"single group", []string{"t_priority"}, []int64{7}},
		{"union is deduped and ascending", []string{"t_vans", "t_priority"}, []int64{7, 9}},
		{"order of the group ids does not matter", []string{"t_priority", "t_vans"}, []int64{7, 9}},
		{"duplicate group ids collapse", []string{"t_vans", "t_vans"}, []int64{7, 9}},
		{"a group with no vehicles contributes nothing", []string{"t_empty"}, []int64{}},
		{"an unknown group id contributes nothing", []string{"t_nope"}, []int64{}},
		{"unknown ids do not hide the known ones", []string{"t_nope", "t_priority"}, []int64{7}},
		{"no groups at all is the empty set", []string{}, []int64{}},
		{"nil groups is the empty set", nil, []int64{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idx.TokenIDsForGroups(tt.groupIDs)
			assert.Equal(t, tt.want, got)
			assert.NotNil(t, got,
				"never nil: an empty union must be expressible as a filter, not skippable")
			assert.Equal(t, len(tt.want), idx.CountForGroups(tt.groupIDs))
		})
	}
}

func TestGroupIndexPerVehicleLookups(t *testing.T) {
	idx := NewGroupIndex(indexFixture())
	priority := GroupRef{ID: "t_priority", Name: "Priority", Color: "#111111"}
	vans := GroupRef{ID: "t_vans", Name: "Vans", Color: "#222222"}

	t.Run("a vehicle in several groups gets them name-ordered", func(t *testing.T) {
		// Priority before Vans, though the wire order was the reverse.
		assert.Equal(t, []GroupRef{priority, vans}, idx.GroupsForVehicle(7))
	})

	t.Run("a vehicle in one group", func(t *testing.T) {
		assert.Equal(t, []GroupRef{vans}, idx.GroupsForVehicle(9))
	})

	t.Run("a vehicle in no group", func(t *testing.T) {
		assert.Empty(t, idx.GroupsForVehicle(404))
	})

	t.Run("VehicleGroupsMap covers exactly the grouped vehicles", func(t *testing.T) {
		m := idx.VehicleGroupsMap()
		require.Len(t, m, 2)
		assert.Equal(t, []GroupRef{priority, vans}, m[7])
		assert.Equal(t, []GroupRef{vans}, m[9])
	})

	t.Run("MemberTokenIDs is deduped and ascending", func(t *testing.T) {
		assert.Equal(t, []int64{7, 9}, idx.MemberTokenIDs("t_vans"))
		assert.Empty(t, idx.MemberTokenIDs("t_empty"))
		assert.Empty(t, idx.MemberTokenIDs("t_nope"))
	})

	t.Run("VehicleInGroups", func(t *testing.T) {
		assert.True(t, idx.VehicleInGroups(7, []string{"t_vans"}))
		assert.True(t, idx.VehicleInGroups(7, []string{"t_nope", "t_priority"}))
		assert.False(t, idx.VehicleInGroups(9, []string{"t_priority"}))
		assert.False(t, idx.VehicleInGroups(7, nil),
			"no groups is 'no' — it must never read as unrestricted")
	})
}

func TestGroupIndexExistence(t *testing.T) {
	idx := NewGroupIndex(indexFixture())

	assert.True(t, idx.AllExist([]string{"t_vans", "t_empty"}))
	assert.False(t, idx.AllExist([]string{"t_vans", "t_nope"}))
	assert.True(t, idx.AllExist(nil), "nothing to check is not a failure")

	ref, ok := idx.Get("t_priority")
	require.True(t, ok)
	assert.Equal(t, GroupRef{ID: "t_priority", Name: "Priority", Color: "#111111"}, ref)

	_, ok = idx.Get("t_nope")
	assert.False(t, ok)
}

// The index is shared between concurrent requests out of the cache, so a caller
// that mutates what it was handed would corrupt every later reader.
func TestGroupIndexHandsOutCopies(t *testing.T) {
	idx := NewGroupIndex(indexFixture())

	idx.MemberTokenIDs("t_vans")[0] = 999
	assert.Equal(t, []int64{7, 9}, idx.MemberTokenIDs("t_vans"))

	idx.GroupsForVehicle(7)[0] = GroupRef{ID: "clobbered"}
	assert.Equal(t, "t_priority", idx.GroupsForVehicle(7)[0].ID)
}

// ----- The trap -----

// A limited member whose groups hold no vehicles must see ZERO vehicles.
//
// The failure this guards is an inversion, not an omission: `if len(ids) > 0 {
// apply filter }` reads as a harmless optimisation and silently promotes such a
// member to the whole fleet — which is what gave 131 memberships a 524-vehicle
// fleet during the backfill. So the assertion is that a filter is produced *at
// all* for the empty set, and that it is one Postgres evaluates to false for
// every row.
func TestLimitedMemberWithVehicleLessGroupsSeesZeroVehicles(t *testing.T) {
	svc := &VehicleService{groups: indexedGroupSource(t, indexFixture())}
	tenant := models.Tenant{ID: "t"}
	ctx := context.Background()

	t.Run("groups that exist but hold no vehicles", func(t *testing.T) {
		accessible, err := svc.AccessibleTokenIDs(ctx, tenant, []string{"t_empty"})
		require.NoError(t, err)
		assert.Empty(t, accessible)

		filter, err := svc.scopeFilter(ctx, tenant, []string{"t_empty"})
		require.NoError(t, err)
		assertMatchesNoVehicle(t, filter)
	})

	t.Run("a scope of no groups at all", func(t *testing.T) {
		accessible, err := svc.AccessibleTokenIDs(ctx, tenant, []string{})
		require.NoError(t, err)
		assert.Empty(t, accessible)

		filter, err := svc.scopeFilter(ctx, tenant, []string{})
		require.NoError(t, err)
		assertMatchesNoVehicle(t, filter)
	})

	t.Run("a scope naming only groups that do not exist", func(t *testing.T) {
		filter, err := svc.scopeFilter(ctx, tenant, []string{"t_deleted"})
		require.NoError(t, err)
		assertMatchesNoVehicle(t, filter)
	})

	t.Run("a non-empty scope still filters to its own vehicles", func(t *testing.T) {
		filter, err := svc.scopeFilter(ctx, tenant, []string{"t_priority"})
		require.NoError(t, err)
		_, args := renderVehicleQuery(t, filter)
		assert.Equal(t, &pq.Int64Array{7}, args[len(args)-1])
	})
}

// assertMatchesNoVehicle checks that the filter is applied and carries an empty
// token array — `token_id = ANY('{}')`, which is false for every row.
func assertMatchesNoVehicle(t *testing.T, filter qm.QueryMod) {
	t.Helper()
	require.NotNil(t, filter, "an empty scope must still produce a filter")
	sql, args := renderVehicleQuery(t, filter)
	assert.Contains(t, strings.ToLower(sql), "token_id = any(",
		"the empty set must be expressed as a predicate, not by omitting the filter")
	assert.Equal(t, &pq.Int64Array{}, args[len(args)-1],
		"an empty array, not NULL and not an absent argument")
}

func renderVehicleQuery(t *testing.T, filter qm.QueryMod) (string, []any) {
	t.Helper()
	q := dbmodels.Vehicles(qm.Where("tenant_id = ?", "t"), filter).Query
	sql, args := queries.BuildQuery(q)
	require.NotEmpty(t, args)
	return sql, args
}

// A tenancy failure must reach the caller. Degrading to an unfiltered read is
// the same inversion by another route.
func TestScopeFailurePropagatesRatherThanUnfiltering(t *testing.T) {
	remote := &fakeRemoteGroups{err: errors.New("tenancy api 503")}
	svc := &VehicleService{groups: remoteViewFixture(remote)}
	ctx := context.Background()

	_, err := svc.scopeFilter(ctx, viewTenant, []string{"t_vans"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGroupScopeUnavailable,
		"controllers key their 503 off this sentinel")

	_, err = svc.AccessibleTokenIDs(ctx, viewTenant, []string{"t_vans"})
	assert.ErrorIs(t, err, ErrGroupScopeUnavailable)
}

// With the flag off the local mirror is still the source, so no remote call is
// made at all — the revert path has to keep working until the tables are gone.
func TestUnflaggedScopeStaysOnTheLocalFilter(t *testing.T) {
	remote := &fakeRemoteGroups{groups: indexFixture()}
	l := zerolog.Nop()
	groupSvc := NewFleetGroupService(&l, nil)
	groupSvc.UseTenancy(remote, false)

	svc := &VehicleService{}
	svc.UseGroupIndex(groupSvc)

	filter, err := svc.scopeFilter(context.Background(), viewTenant, []string{"t_empty"})
	require.NoError(t, err)
	assert.Zero(t, remote.calls)

	// Still a filter, and still one an empty set cannot escape: `= ANY('{}')`
	// on the subquery's group ids.
	sql, args := renderVehicleQuery(t, filter)
	assert.Contains(t, sql, "vehicle_fleet_groups")
	assert.Equal(t, &pq.StringArray{"t_empty"}, args[len(args)-1])
}

// ----- The cache -----

func TestGroupIndexCacheServesRepeatedReadsFromOneRemoteCall(t *testing.T) {
	remote := &fakeRemoteGroups{groups: indexFixture()}
	s := remoteViewFixture(remote)
	ctx := context.Background()

	first, err := s.groupIndex(ctx, viewTenant)
	require.NoError(t, err)
	second, err := s.groupIndex(ctx, viewTenant)
	require.NoError(t, err)

	assert.Same(t, first, second)
	assert.Equal(t, 1, remote.calls, "the scope filter runs per request; the endpoint must not")

	// Keyed by tenant: another tenant is a different answer, not a cache hit.
	_, err = s.groupIndex(ctx, models.Tenant{ID: "other-tenant"})
	require.NoError(t, err)
	assert.Equal(t, 2, remote.calls)
}

// invalidateGroupIndex only reaches the process that handled the write, and this
// app runs more than one replica. So a group mutation served by pod A leaves pod
// B's index stale until the TTL, and the operator watching the management screen
// sees their change fail to land on whichever loads the balancer sends to B.
//
// The management reads therefore have to go to tenancy rather than trust the
// cache. The fake standing still while its data changes underneath is exactly
// what a sibling pod's write looks like from here.
func TestManagementViewsDoNotServeAnotherPodsStaleIndex(t *testing.T) {
	remote := &fakeRemoteGroups{groups: indexFixture()}
	s := remoteViewFixture(remote)
	ctx := context.Background()

	// Warm this pod's cache, as any earlier request on it would have.
	_, err := s.groupIndex(ctx, viewTenant)
	require.NoError(t, err)
	require.Equal(t, 1, remote.calls)

	// A sibling pod adds a vehicle to a group. Tenancy now says something new;
	// this pod was never told.
	grown := make([]models.RemoteFleetGroup, len(remote.groups))
	copy(grown, remote.groups)
	grown[0].VehicleCount++
	grown[0].TokenIDs = append(append([]int64{}, grown[0].TokenIDs...), 4242)
	remote.groups = grown

	t.Run("ListGroupsView shows the new count", func(t *testing.T) {
		got, err := s.ListGroupsView(ctx, viewTenant)
		require.NoError(t, err)
		// By id, not position: the view is name-ordered, the fixture isn't.
		var found *GroupWithCount
		for i := range got {
			if got[i].Group.ID == grown[0].ID {
				found = &got[i]
			}
		}
		require.NotNil(t, found, "the grown group should still be listed")
		assert.Equal(t, grown[0].VehicleCount, found.VehicleCount,
			"the operator just made this change; the count has to move")
	})

	t.Run("GetGroupView shows the new member", func(t *testing.T) {
		_, members, err := s.GetGroupView(ctx, viewTenant, grown[0].ID)
		require.NoError(t, err)
		assert.Contains(t, members, int64(4242))
	})

	// The fresh read repairs the entry rather than bypassing it, so the scope
	// filter — the reason the cache exists — is now correct too, and still free.
	t.Run("the repaired cache serves the scope filter without another call", func(t *testing.T) {
		before := remote.calls
		idx, err := s.groupIndex(ctx, viewTenant)
		require.NoError(t, err)
		assert.Equal(t, before, remote.calls, "scope filter must stay a cache read")
		assert.Contains(t, idx.MemberTokenIDs(grown[0].ID), int64(4242))
	})
}

func TestGroupIndexCacheIsBustedByEveryWrite(t *testing.T) {
	name := "Trucks"
	// AddVehicle is absent on purpose: it validates the token id against this
	// app's own vehicles table first, which the nil store cannot serve, so its
	// write path is unreachable here. Its pre-write membership read is covered
	// by TestAddVehicleReadsMembershipFromTheIndex.
	writes := map[string]func(*FleetGroupService) error{
		"CreateGroup": func(s *FleetGroupService) error {
			_, err := s.CreateGroup(context.Background(), viewTenant, "Trucks", "#112233")
			return err
		},
		"UpdateGroup": func(s *FleetGroupService) error {
			_, err := s.UpdateGroup(context.Background(), viewTenant, "t_vans", &name, nil)
			return err
		},
		"DeleteGroup": func(s *FleetGroupService) error {
			return s.DeleteGroup(context.Background(), viewTenant, "t_vans")
		},
		"RemoveVehicle": func(s *FleetGroupService) error {
			_, err := s.RemoveVehicle(context.Background(), viewTenant, 7, "t_vans")
			return err
		},
	}
	for op, write := range writes {
		t.Run(op, func(t *testing.T) {
			remote := &fakeRemoteGroups{groups: indexFixture()}
			s := remoteViewFixture(remote)
			ctx := context.Background()

			_, err := s.groupIndex(ctx, viewTenant)
			require.NoError(t, err)
			require.Equal(t, 1, remote.calls)

			// The mirror write hits a nil store and panics; the bust happens
			// first, which is exactly the ordering under test.
			func() {
				defer func() { _ = recover() }()
				_ = write(s)
			}()

			_, err = s.groupIndex(ctx, viewTenant)
			require.NoError(t, err)
			assert.Equal(t, 2, remote.calls,
				"a write must not leave readers on the pre-write answer for the rest of the TTL")
		})
	}
}

func TestGroupIndexCacheNeverCachesAFailure(t *testing.T) {
	remote := &fakeRemoteGroups{err: errors.New("tenancy api 503")}
	s := remoteViewFixture(remote)
	ctx := context.Background()

	_, err := s.groupIndex(ctx, viewTenant)
	require.ErrorIs(t, err, ErrGroupScopeUnavailable)

	// The outage clears; the very next read must see it, not serve the failure
	// for the rest of the window.
	remote.err = nil
	remote.groups = indexFixture()
	idx, err := s.groupIndex(ctx, viewTenant)
	require.NoError(t, err)
	assert.Equal(t, []int64{7, 9}, idx.MemberTokenIDs("t_vans"))
	assert.Equal(t, 2, remote.calls)
}

// indexedGroupSource is a FleetGroupService wired to a fixed group set with the
// flag on — the P5 read path, without a tenancy service or a database.
func indexedGroupSource(t *testing.T, groups []models.RemoteFleetGroup) groupIndexSource {
	t.Helper()
	l := zerolog.Nop()
	s := NewFleetGroupService(&l, nil)
	s.UseTenancy(&fakeRemoteGroups{groups: groups}, true)
	return s
}

// AddVehicle answers "did this call add it?" from the authority, not from the
// mirror, once the flag is on.
func TestAddVehicleReadsMembershipFromTheIndex(t *testing.T) {
	remote := &fakeRemoteGroups{groups: indexFixture()}
	s := remoteViewFixture(remote)
	ctx := context.Background()

	existed, err := s.membershipExists(ctx, viewTenant, 7, "t_vans")
	require.NoError(t, err)
	assert.True(t, existed)

	existed, err = s.membershipExists(ctx, viewTenant, 7, "t_empty")
	require.NoError(t, err)
	assert.False(t, existed)

	assert.Equal(t, 1, remote.calls, "both answers come from one cached index")
}
