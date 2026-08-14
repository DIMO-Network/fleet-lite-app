package controllers

import (
	"encoding/json"
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
)

// A vehicle is reachable through any one of its groups, so a limited member can
// hold a vehicle that also sits in groups outside their scope. Both /vehicles
// and /vehicles/:tokenID attach group refs, and neither may hand back the name
// of a group the member isn't scoped to — that's a disclosure, not a display
// bug. The failure direction matters too: when the group load fails we narrow to
// empty, never widen to everything.
func TestScopeGroupRefs(t *testing.T) {
	refs := func() []models.GroupRef {
		return []models.GroupRef{
			{ID: "g1", Name: "North", Color: "#111"},
			{ID: "g2", Name: "Secret Skunkworks", Color: "#222"},
			{ID: "g3", Name: "South", Color: "#333"},
		}
	}

	t.Run("limited member sees only allowed groups", func(t *testing.T) {
		got := scopeGroupRefs(refs(), true, []string{"g1", "g3"})
		if len(got) != 2 {
			t.Fatalf("len = %d; want 2", len(got))
		}
		for _, g := range got {
			if g.ID == "g2" {
				t.Errorf("leaked out-of-scope group %q (%s)", g.ID, g.Name)
			}
		}
	})

	t.Run("full-access member sees every group", func(t *testing.T) {
		if got := scopeGroupRefs(refs(), false, nil); len(got) != 3 {
			t.Errorf("len = %d; want 3", len(got))
		}
	})

	t.Run("limited member with no allowed groups sees none", func(t *testing.T) {
		if got := scopeGroupRefs(refs(), true, nil); len(got) != 0 {
			t.Errorf("len = %d; want 0", len(got))
		}
	})

	// nil means the group load failed. Narrowing to empty is wrong-but-safe;
	// widening to the full set would hand a limited member the whole structure.
	t.Run("nil groups narrow to empty, not to everything", func(t *testing.T) {
		if got := scopeGroupRefs(nil, true, []string{"g1"}); len(got) != 0 {
			t.Errorf("len = %d; want 0", len(got))
		}
		if got := scopeGroupRefs(nil, false, nil); len(got) != 0 {
			t.Errorf("len = %d; want 0", len(got))
		}
	})

	// The frontend guards on length, but `groups: null` in the payload is still a
	// contract break — Vehicle.groups is a required array on the TS side.
	t.Run("always serializes as an array, never null", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			groups  []models.GroupRef
			limited bool
			allowed []string
		}{
			{"nil groups", nil, false, nil},
			{"limited filters everything out", refs(), true, []string{"nope"}},
		} {
			b, err := json.Marshal(scopeGroupRefs(tc.groups, tc.limited, tc.allowed))
			if err != nil {
				t.Fatalf("%s: marshal: %v", tc.name, err)
			}
			if string(b) != "[]" {
				t.Errorf("%s: marshaled %s; want []", tc.name, b)
			}
		}
	})

	// The filter aliases the input's backing array; callers in the /vehicles loop
	// reuse the map values, so a write-through would corrupt a sibling vehicle's
	// refs.
	t.Run("does not clobber the caller's slice", func(t *testing.T) {
		in := refs()
		scopeGroupRefs(in, true, []string{"g3"})
		if in[0].ID != "g1" || in[1].ID != "g2" || in[2].ID != "g3" {
			t.Errorf("input mutated: %+v", in)
		}
	})
}
