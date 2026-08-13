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
// TenantOwnsGroupID distinguish legacy from tenant-scoped ids without a lookup
// — if it ever breaks, the ownership rule silently misclassifies.
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
