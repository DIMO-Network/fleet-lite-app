package main

import (
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The remote answer is the authority: everything it has appears locally,
// everything it lacks disappears locally — except memberships whose vehicle
// this app has not synced, which are skipped, not failed (the composite FK to
// vehicles(tenant_id, token_id) makes them uninsertable).
func TestPlanMirror(t *testing.T) {
	local := map[string]localGroupState{
		"t_agree":   {Name: "Agree", Color: "#111111", TokenIDs: []int64{1, 2}},
		"t_renamed": {Name: "Old Name", Color: "#222222", TokenIDs: []int64{3}},
		"t_gone":    {Name: "Gone", Color: "#333333", TokenIDs: []int64{4, 5}},
	}
	remote := []models.RemoteFleetGroup{
		{ID: "t_agree", Name: "Agree", Color: "#111111", TokenIDs: []int64{2, 1}},
		{ID: "t_renamed", Name: "New Name", Color: "#222222", TokenIDs: []int64{3, 6}},
		{ID: "t_new", Name: "New", Color: "#444444", TokenIDs: []int64{7, 99}},
	}
	haveVehicle := map[int64]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true}

	plan := planMirror(local, remote, haveVehicle)

	assert.Equal(t, []groupMeta{{ID: "t_new", Name: "New", Color: "#444444"}}, plan.GroupsToInsert)
	assert.Equal(t, []groupMeta{{ID: "t_renamed", Name: "New Name", Color: "#222222"}}, plan.GroupsToUpdate,
		"metadata follows the authority")
	assert.Equal(t, []string{"t_gone"}, plan.GroupIDsToDelete)
	assert.Equal(t, []membershipRef{
		{GroupID: "t_new", TokenID: 7},
		{GroupID: "t_renamed", TokenID: 6},
	}, plan.MembershipsToInsert)
	assert.Equal(t, []membershipRef{
		{GroupID: "t_gone", TokenID: 4},
		{GroupID: "t_gone", TokenID: 5},
	}, plan.MembershipsToDelete,
		"a deleted group's rows are counted removals, not silent cascade casualties")
	assert.Equal(t, 1, plan.SkippedNoVehicle, "token 99 is not a synced vehicle")
	assert.False(t, plan.empty())
}

func TestPlanMirrorConvergedIsEmpty(t *testing.T) {
	local := map[string]localGroupState{
		"t_vans": {Name: "Vans", Color: "#111111", TokenIDs: []int64{1, 2}},
	}
	remote := []models.RemoteFleetGroup{
		{ID: "t_vans", Name: "Vans", Color: "#111111", TokenIDs: []int64{1, 2}},
	}
	plan := planMirror(local, remote, map[int64]bool{1: true, 2: true})
	assert.True(t, plan.empty(), "a converged tenant plans no writes")
	assert.Zero(t, plan.SkippedNoVehicle)
}

// Member order is not a difference, and a duplicate token id in the remote
// answer must not plan a duplicate insert.
func TestPlanMirrorOrderAndDuplicates(t *testing.T) {
	local := map[string]localGroupState{
		"t_g": {Name: "G", Color: "#111111", TokenIDs: []int64{2, 1}},
	}
	remote := []models.RemoteFleetGroup{
		{ID: "t_g", Name: "G", Color: "#111111", TokenIDs: []int64{1, 2, 3, 3}},
	}
	plan := planMirror(local, remote, map[int64]bool{1: true, 2: true, 3: true})
	require.Equal(t, []membershipRef{{GroupID: "t_g", TokenID: 3}}, plan.MembershipsToInsert)
	assert.Empty(t, plan.MembershipsToDelete)
	assert.Empty(t, plan.GroupsToUpdate)
}

// An unsynced vehicle's membership is skipped on insert, but an existing local
// row whose vehicle vanished from the remote set is still deleted — skipping
// only ever narrows inserts.
func TestPlanMirrorSkipsOnlyInserts(t *testing.T) {
	local := map[string]localGroupState{
		"t_g": {Name: "G", Color: "#111111", TokenIDs: []int64{1}},
	}
	remote := []models.RemoteFleetGroup{
		{ID: "t_g", Name: "G", Color: "#111111", TokenIDs: []int64{8}},
	}
	plan := planMirror(local, remote, map[int64]bool{1: true})
	assert.Empty(t, plan.MembershipsToInsert)
	assert.Equal(t, 1, plan.SkippedNoVehicle)
	assert.Equal(t, []membershipRef{{GroupID: "t_g", TokenID: 1}}, plan.MembershipsToDelete)
}

// Both sides empty: nothing to do, nothing to report.
func TestPlanMirrorBothEmpty(t *testing.T) {
	plan := planMirror(map[string]localGroupState{}, nil, map[int64]bool{})
	assert.True(t, plan.empty())
}
