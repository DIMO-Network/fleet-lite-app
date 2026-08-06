package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupByTenant(t *testing.T) {
	const (
		tenantA = "7be1ab9e-9286-4a8f-b45f-15f25ee4da77"
		tenantB = "5d9fc2e2-7c24-478a-a353-3d04cfe0c28c"
	)

	t.Run("buckets vehicles by tenant and preserves first-appearance order", func(t *testing.T) {
		byTenant, order := groupByTenant([]vehicleRef{
			{TenantID: tenantA, TokenID: 1},
			{TenantID: tenantA, TokenID: 2},
			{TenantID: tenantB, TokenID: 3},
			{TenantID: tenantA, TokenID: 4},
		})

		require.Equal(t, []string{tenantA, tenantB}, order,
			"tenant order must be first appearance, so a re-run over unchanged data matches")
		assert.Equal(t, []int64{1, 2, 4}, byTenant[tenantA])
		assert.Equal(t, []int64{3}, byTenant[tenantB])
	})

	t.Run("every vehicle lands in exactly one bucket", func(t *testing.T) {
		refs := []vehicleRef{
			{TenantID: tenantA, TokenID: 10},
			{TenantID: tenantB, TokenID: 11},
			{TenantID: tenantA, TokenID: 12},
		}
		byTenant, order := groupByTenant(refs)

		total := 0
		for _, ids := range byTenant {
			total += len(ids)
		}
		assert.Equal(t, len(refs), total,
			"a dropped vehicle is one the foreign-drop would later strip, so none may go missing")
		assert.Len(t, order, len(byTenant), "order must name each bucket exactly once")
	})

	t.Run("empty input yields empty output rather than nil-map panics", func(t *testing.T) {
		byTenant, order := groupByTenant(nil)
		assert.Empty(t, order)
		assert.Empty(t, byTenant)
	})

	t.Run("a single tenant with one vehicle is not a special case", func(t *testing.T) {
		byTenant, order := groupByTenant([]vehicleRef{{TenantID: tenantA, TokenID: 99}})
		require.Equal(t, []string{tenantA}, order)
		assert.Equal(t, []int64{99}, byTenant[tenantA])
	})
}
