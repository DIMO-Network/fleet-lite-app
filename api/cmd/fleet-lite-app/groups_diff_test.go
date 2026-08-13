package main

import (
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func findingsByID(fs []groupFinding) map[string]groupFinding {
	out := map[string]groupFinding{}
	for _, f := range fs {
		out[f.GroupID] = f
	}
	return out
}

// The asymmetry is the load-bearing part, inherited from tenancy-diff: tenancy
// holds the union of both source systems, so remote-extra is expected and
// informational, while missing-remote and differ are failures.
func TestCompareGroupSets(t *testing.T) {
	local := map[string]localGroupState{
		"t_agree":   {Name: "Agree", Color: "#111111", TokenIDs: []int64{1, 2}},
		"t_renamed": {Name: "New Name", Color: "#222222", TokenIDs: []int64{3}},
		"t_local":   {Name: "Local Only", Color: "#333333"},
		"t_short":   {Name: "Short", Color: "#444444", TokenIDs: []int64{4, 5}},
		"t_extra":   {Name: "Extra", Color: "#555555", TokenIDs: []int64{6}},
	}
	remote := []models.RemoteFleetGroup{
		{ID: "t_agree", Name: "Agree", Color: "#111111", TokenIDs: []int64{2, 1}},
		{ID: "t_renamed", Name: "Old Name", Color: "#222222", TokenIDs: []int64{3}},
		{ID: "t_short", Name: "Short", Color: "#444444", TokenIDs: []int64{4}},
		{ID: "t_extra", Name: "Extra", Color: "#555555", TokenIDs: []int64{6, 7}},
		{ID: "t_remote", Name: "Remote Only", Color: "#666666"},
	}

	fs := findingsByID(compareGroupSets(local, remote))
	require.Len(t, fs, 6)

	assert.Equal(t, groupAgree, fs["t_agree"].Verdict, "member order is not a difference")
	assert.Equal(t, groupDiffer, fs["t_renamed"].Verdict, "metadata disagreement is a failure")
	assert.Equal(t, groupMissingRemote, fs["t_local"].Verdict, "a local group tenancy lacks is a failure")
	assert.Equal(t, groupMissingRemote, fs["t_short"].Verdict, "a local member tenancy lacks is a failure")
	assert.Contains(t, fs["t_short"].Detail, "[5]")
	assert.Equal(t, groupRemoteExtra, fs["t_extra"].Verdict, "a member only in tenancy is informational")
	assert.Equal(t, groupRemoteExtra, fs["t_remote"].Verdict, "a group only in tenancy is informational")
}

func TestCompareGroupSetsBothEmpty(t *testing.T) {
	assert.Empty(t, compareGroupSets(map[string]localGroupState{}, nil),
		"a tenant with no groups anywhere has nothing to report")
}

// A group missing members remotely AND holding remote extras reports the
// failure, not the info — missing-remote must never be masked.
func TestCompareGroupSetsMissingBeatsExtra(t *testing.T) {
	fs := compareGroupSets(
		map[string]localGroupState{"t_g": {Name: "G", Color: "#111111", TokenIDs: []int64{1}}},
		[]models.RemoteFleetGroup{{ID: "t_g", Name: "G", Color: "#111111", TokenIDs: []int64{2}}},
	)
	require.Len(t, fs, 1)
	assert.Equal(t, groupMissingRemote, fs[0].Verdict)
}
