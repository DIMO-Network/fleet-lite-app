package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

func TestGroupID(t *testing.T) {
	assert.Equal(t, tenantA+"_vans", GroupID(tenantA, "Vans"))
	assert.Equal(t, tenantA+"_west-coast", GroupID(tenantA, "West Coast"))
	assert.Equal(t, tenantA+"_a-b", GroupID(tenantA, "  A & B!  "))
	assert.Empty(t, GroupID(tenantA, "!!!"), "a name with no alphanumerics has no id")

	// The collision this fixes: same name, different tenants, distinct ids.
	assert.NotEqual(t, GroupID(tenantA, "Vans"), GroupID(tenantB, "Vans"))
}

// A slug can never contain '_' (slugNonAlphanum collapses non-alphanumerics to
// '-') and a uuid never contains '_'. That invariant is what lets
// normaliseGroupID distinguish legacy from tenant-scoped ids without a lookup —
// if it ever breaks, the acceptance rule silently misclassifies.
func TestSlugNeverContainsSeparator(t *testing.T) {
	for _, name := range []string{"a_b", "Snake_Case_Name", "___", "x_1 y_2"} {
		assert.NotContains(t, slug(name), GroupIDSeparator, "slug(%q)", name)
	}
}

func TestTenantOwnsGroupID(t *testing.T) {
	assert.True(t, TenantOwnsGroupID(tenantA, tenantA+"_vans"))
	assert.False(t, TenantOwnsGroupID(tenantA, tenantB+"_vans"))
	assert.False(t, TenantOwnsGroupID(tenantA, "vans"), "legacy bare slug carries no tenant")
	assert.False(t, TenantOwnsGroupID(tenantA, ""))
}

func TestNormaliseGroupID(t *testing.T) {
	for _, tc := range []struct {
		name        string
		id          string
		dropForeign bool
		wantID      string
		wantOK      bool
	}{
		{"legacy bare slug is adopted", "vans", false, tenantA + "_vans", true},
		{"legacy bare slug adopted even when dropping foreign", "vans", true, tenantA + "_vans", true},
		{"own tenant passes through", tenantA + "_vans", false, tenantA + "_vans", true},
		{"own tenant passes through when dropping", tenantA + "_vans", true, tenantA + "_vans", true},
		{"foreign tenant adopted while flag off", tenantB + "_vans", false, tenantA + "_vans", true},
		{"foreign tenant dropped while flag on", tenantB + "_vans", true, "", false},
		{"hyphenated slug survives", tenantB + "_west-coast", false, tenantA + "_west-coast", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotOK := normaliseGroupID(tenantA, tc.id, tc.dropForeign)
			assert.Equal(t, tc.wantOK, gotOK)
			assert.Equal(t, tc.wantID, gotID)
		})
	}
}

// The transitional branch must reproduce today's implicit behaviour exactly:
// before ids carried a tenant, a foreign "vans" and a local "vans" were the same
// row. With the flag off they must still converge on one id, or the migration
// would silently duplicate every group.
func TestNormalise_ForeignAndLocalConvergeWhileFlagOff(t *testing.T) {
	fromForeign, ok1 := normaliseGroupID(tenantA, tenantB+"_vans", false)
	fromLegacy, ok2 := normaliseGroupID(tenantA, "vans", false)
	fromOwn, ok3 := normaliseGroupID(tenantA, tenantA+"_vans", false)
	assert.True(t, ok1 && ok2 && ok3)
	assert.Equal(t, fromOwn, fromForeign)
	assert.Equal(t, fromOwn, fromLegacy)
}
