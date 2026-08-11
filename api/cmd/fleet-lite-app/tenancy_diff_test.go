package main

import (
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/stretchr/testify/assert"
)

// The verdict a comparison produces decides whether a human investigates before
// cutover, so each class is pinned explicitly.
func TestCompareAccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		local  localAccess
		remote *gateway.AuthzResult
		want   diffVerdict
	}{
		{
			name:   "identical owner",
			local:  localAccess{Role: "owner", Permissions: []string{"manage_members", "manage_settings"}, Unrestricted: true},
			remote: &gateway.AuthzResult{Via: "direct", Role: "owner", Permissions: []string{"manage_members", "manage_settings"}},
			want:   verdictAgree,
		},
		{
			name:   "identical plain member",
			local:  localAccess{Role: "member", Unrestricted: true},
			remote: &gateway.AuthzResult{Via: "direct", Role: "member", Permissions: []string{}},
			want:   verdictAgree,
		},
		{
			name:   "ordering and casing are not differences",
			local:  localAccess{Role: "owner", Permissions: []string{"Manage_Settings", "manage_members"}, Unrestricted: true},
			remote: &gateway.AuthzResult{Via: "direct", Role: "owner", Permissions: []string{"manage_members", "manage_settings"}},
			want:   verdictAgree,
		},
		{
			name:   "no remote access at all",
			local:  localAccess{Role: "owner", Permissions: []string{"manage_members"}, Unrestricted: true},
			remote: &gateway.AuthzResult{Via: "none"},
			want:   verdictMissingRemote,
		},
		{
			name:   "remote lost a capability",
			local:  localAccess{Role: "owner", Permissions: []string{"manage_members", "manage_settings"}, Unrestricted: true},
			remote: &gateway.AuthzResult{Via: "direct", Role: "owner", Permissions: []string{"manage_members"}},
			want:   verdictDiffer,
		},
		{
			// The Kaufmann-tenant overlap: the service holds the merge of both
			// sources, so this side alone legitimately looks smaller.
			name:   "remote grants more (merge)",
			local:  localAccess{Role: "member", Unrestricted: true},
			remote: &gateway.AuthzResult{Via: "direct", Role: "member", Permissions: []string{"reports"}},
			want:   verdictRemoteExtra,
		},
		{
			name:   "role label differs only",
			local:  localAccess{Role: "member", Unrestricted: true},
			remote: &gateway.AuthzResult{Via: "direct", Role: "admin", Permissions: []string{}},
			want:   verdictRoleDiffers,
		},
		{
			// The inversion that made the backfill omission dangerous: local
			// sees everything, remote sees only two groups. A narrowing.
			name:   "local unrestricted, remote restricted",
			local:  localAccess{Role: "member", Unrestricted: true},
			remote: &gateway.AuthzResult{Via: "direct", Role: "member", ScopeGroupIDs: []string{"t_a", "t_b"}},
			want:   verdictDiffer,
		},
		{
			// Empty (not nil) means restricted to nothing — still a narrowing
			// against an unrestricted local, and the case a len() check misses.
			name:   "local unrestricted, remote sees nothing",
			local:  localAccess{Role: "member", Unrestricted: true},
			remote: &gateway.AuthzResult{Via: "direct", Role: "member", ScopeGroupIDs: []string{}},
			want:   verdictDiffer,
		},
		{
			name:   "local restricted, remote unrestricted",
			local:  localAccess{Role: "member", ScopeGroups: []string{"t_a"}},
			remote: &gateway.AuthzResult{Via: "direct", Role: "member"},
			want:   verdictRemoteExtra,
		},
		{
			name:   "same restricted group set",
			local:  localAccess{Role: "member", ScopeGroups: []string{"t_b", "t_a"}},
			remote: &gateway.AuthzResult{Via: "direct", Role: "member", ScopeGroupIDs: []string{"t_a", "t_b"}},
			want:   verdictAgree,
		},
		{
			name:   "remote dropped a group",
			local:  localAccess{Role: "member", ScopeGroups: []string{"t_a", "t_b"}},
			remote: &gateway.AuthzResult{Via: "direct", Role: "member", ScopeGroupIDs: []string{"t_a"}},
			want:   verdictDiffer,
		},
		{
			name:   "remote added a group",
			local:  localAccess{Role: "member", ScopeGroups: []string{"t_a"}},
			remote: &gateway.AuthzResult{Via: "direct", Role: "member", ScopeGroupIDs: []string{"t_a", "t_b"}},
			want:   verdictRemoteExtra,
		},
		{
			// A narrowing must win over a widening — otherwise a row that both
			// gains and loses reads as benign.
			name:   "narrowing outranks widening",
			local:  localAccess{Role: "member", Permissions: []string{"reports"}, Unrestricted: true},
			remote: &gateway.AuthzResult{Via: "direct", Role: "member", Permissions: []string{"onboard_vehicles"}},
			want:   verdictDiffer,
		},
		{
			name:   "nil remote is missing, not a panic",
			local:  localAccess{Role: "owner", Unrestricted: true},
			remote: nil,
			want:   verdictMissingRemote,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, detail := compareAccess(tc.local, tc.remote)
			assert.Equal(t, tc.want, got, "detail: %s", detail)
			if got != verdictAgree {
				assert.NotEmpty(t, detail, "a non-agreeing verdict must explain itself")
			}
		})
	}
}

// The local mapping must reproduce the backfill's, or the diff measures the
// wrong thing. Owners get exactly the two capabilities fleet-lite gates on.
func TestFleetLiteLocalAccess(t *testing.T) {
	owner := fleetLiteLocalAccess("owner", nil)
	assert.ElementsMatch(t, []string{"manage_members", "manage_settings"}, owner.Permissions)
	assert.True(t, owner.Unrestricted)

	member := fleetLiteLocalAccess("member", nil)
	assert.Empty(t, member.Permissions)
	assert.True(t, member.Unrestricted)

	// A NULL column is unrestricted; an empty array is restricted to nothing.
	// Collapsing these is the exact bug this whole command exists to catch.
	limited := fleetLiteLocalAccess("member", []string{})
	assert.False(t, limited.Unrestricted)

	scoped := fleetLiteLocalAccess("member", []string{"t_a"})
	assert.False(t, scoped.Unrestricted)
	assert.Equal(t, []string{"t_a"}, scoped.ScopeGroups)
}

func TestNormalizeSetAndDifference(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, normalizeSet([]string{"B", " a ", "a", ""}))
	assert.Equal(t, []string{}, setDifference([]string{"a"}, []string{"a", "b"}))
	assert.Equal(t, []string{"b"}, setDifference([]string{"a", "b"}, []string{"a"}))
}
