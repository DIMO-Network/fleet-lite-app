package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/lib/pq"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMembershipGate implements membershipGate for filter tests.
type fakeMembershipGate struct {
	enforced bool
	tokens   []int64
	err      error
}

func (f *fakeMembershipGate) ActiveTokens(context.Context, models.Tenant) (bool, []int64, error) {
	return f.enforced, f.tokens, f.err
}

// fakeMembershipRemote implements remoteMembershipSource for cache tests.
type fakeMembershipRemote struct {
	active *models.RemoteActiveMemberships
	err    error
	calls  int
}

func (f *fakeMembershipRemote) ActiveVehicleMemberships(context.Context, models.Tenant) (*models.RemoteActiveMemberships, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	// A fresh copy per call, as JSON decoding would produce.
	cp := *f.active
	return &cp, f.err
}

func (f *fakeMembershipRemote) VehicleMemberships(context.Context, models.Tenant) (*models.RemoteMembershipList, error) {
	return nil, errors.New("not under test")
}

// The membership gate applies to OWNERS, not just limited members — that is
// the asymmetry that separates it from scopeFilter, and the one that would
// otherwise reach production: an owner-account test passes while the feature
// is half-off. These run against a nil DB store on purpose: the gate must
// answer (or fail) BEFORE any query runs, so a degrade-to-unfiltered here
// would panic on the nil store instead of returning the sentinel.
func TestMembershipGateAppliesToOwners(t *testing.T) {
	ctx := context.Background()
	failing := &fakeMembershipGate{err: ErrMembershipScopeUnavailable}

	t.Run("ListVehicles for an owner fails closed", func(t *testing.T) {
		svc := &VehicleService{memberships: failing}
		_, err := svc.ListVehicles(ctx, viewTenant, nil) // nil = unrestricted caller
		assert.ErrorIs(t, err, ErrMembershipScopeUnavailable,
			"the gate must run for unrestricted callers; controllers key their 503 off this sentinel")
	})

	t.Run("GetVehicle for an owner fails closed", func(t *testing.T) {
		svc := &VehicleService{memberships: failing}
		_, err := svc.GetVehicle(ctx, viewTenant, 7, nil)
		assert.ErrorIs(t, err, ErrMembershipScopeUnavailable)
	})
}

func TestMembershipFilter(t *testing.T) {
	ctx := context.Background()

	t.Run("no gate wired means no filter at all", func(t *testing.T) {
		svc := &VehicleService{}
		filter, err := svc.membershipFilter(ctx, viewTenant)
		require.NoError(t, err)
		assert.Nil(t, filter)
	})

	t.Run("enforcement off means no filter, membership state irrelevant", func(t *testing.T) {
		svc := &VehicleService{memberships: &fakeMembershipGate{enforced: false, tokens: []int64{1, 2}}}
		filter, err := svc.membershipFilter(ctx, viewTenant)
		require.NoError(t, err)
		assert.Nil(t, filter)
	})

	t.Run("enforcement on restricts to exactly the membered set", func(t *testing.T) {
		svc := &VehicleService{memberships: &fakeMembershipGate{enforced: true, tokens: []int64{7, 9}}}
		filter, err := svc.membershipFilter(ctx, viewTenant)
		require.NoError(t, err)
		require.NotNil(t, filter)
		_, args := renderVehicleQuery(t, filter)
		assert.Equal(t, &pq.Int64Array{7, 9}, args[len(args)-1])
	})

	t.Run("enforcement on with zero memberships matches zero vehicles", func(t *testing.T) {
		svc := &VehicleService{memberships: &fakeMembershipGate{enforced: true, tokens: []int64{}}}
		filter, err := svc.membershipFilter(ctx, viewTenant)
		require.NoError(t, err)
		assertMatchesNoVehicle(t, filter)
	})

	t.Run("a gate failure propagates rather than unfiltering", func(t *testing.T) {
		svc := &VehicleService{memberships: &fakeMembershipGate{err: ErrMembershipScopeUnavailable}}
		_, err := svc.membershipFilter(ctx, viewTenant)
		assert.ErrorIs(t, err, ErrMembershipScopeUnavailable)
	})
}

// Geofence counts read AccessibleTokenIDs, so a limited member's group scope
// has to be intersected with the membered set — otherwise those screens count
// vehicles the fleet no longer shows (#115's leak, by another route).
func TestAccessibleTokenIDsIntersectsActiveMemberships(t *testing.T) {
	ctx := context.Background()

	t.Run("enforced narrows the group scope to the paid subset", func(t *testing.T) {
		svc := &VehicleService{
			groups:      indexedGroupSource(t, indexFixture()),
			memberships: &fakeMembershipGate{enforced: true, tokens: []int64{7}},
		}
		accessible, err := svc.AccessibleTokenIDs(ctx, viewTenant, []string{"t_vans"})
		require.NoError(t, err)
		assert.Equal(t, map[int64]bool{7: true}, accessible,
			"9 is in the group but unmembered; only 7 survives")
	})

	t.Run("unenforced leaves the group scope alone", func(t *testing.T) {
		svc := &VehicleService{
			groups:      indexedGroupSource(t, indexFixture()),
			memberships: &fakeMembershipGate{enforced: false, tokens: []int64{}},
		}
		accessible, err := svc.AccessibleTokenIDs(ctx, viewTenant, []string{"t_vans"})
		require.NoError(t, err)
		assert.Equal(t, map[int64]bool{7: true, 9: true}, accessible)
	})

	t.Run("enforced with no memberships empties the set, never widens it", func(t *testing.T) {
		svc := &VehicleService{
			groups:      indexedGroupSource(t, indexFixture()),
			memberships: &fakeMembershipGate{enforced: true, tokens: []int64{}},
		}
		accessible, err := svc.AccessibleTokenIDs(ctx, viewTenant, []string{"t_vans"})
		require.NoError(t, err)
		assert.Empty(t, accessible)
	})

	t.Run("a gate failure propagates", func(t *testing.T) {
		svc := &VehicleService{
			groups:      indexedGroupSource(t, indexFixture()),
			memberships: &fakeMembershipGate{err: ErrMembershipScopeUnavailable},
		}
		_, err := svc.AccessibleTokenIDs(ctx, viewTenant, []string{"t_vans"})
		assert.ErrorIs(t, err, ErrMembershipScopeUnavailable)
	})
}

func TestMembershipServiceCache(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	t.Run("repeated reads are one remote call", func(t *testing.T) {
		remote := &fakeMembershipRemote{active: &models.RemoteActiveMemberships{Enforced: true, TokenIDs: []int64{7}}}
		svc := NewMembershipService(&logger, remote)

		for i := 0; i < 3; i++ {
			enforced, tokens, err := svc.ActiveTokens(ctx, viewTenant)
			require.NoError(t, err)
			assert.True(t, enforced)
			assert.Equal(t, []int64{7}, tokens)
		}
		assert.Equal(t, 1, remote.calls,
			"this runs per vehicle-list request; the endpoint must not")

		// Keyed by tenant: another tenant is a different answer, not a hit.
		_, _, err := svc.ActiveTokens(ctx, models.Tenant{ID: "other"})
		require.NoError(t, err)
		assert.Equal(t, 2, remote.calls)
	})

	t.Run("failures are never cached", func(t *testing.T) {
		remote := &fakeMembershipRemote{err: errors.New("tenancy 503")}
		svc := NewMembershipService(&logger, remote)

		_, _, err := svc.ActiveTokens(ctx, viewTenant)
		assert.ErrorIs(t, err, ErrMembershipScopeUnavailable)
		_, _, err = svc.ActiveTokens(ctx, viewTenant)
		assert.ErrorIs(t, err, ErrMembershipScopeUnavailable)
		assert.Equal(t, 2, remote.calls,
			"a cached rejection would extend an outage past its cause")
	})

	t.Run("a null token list is pinned to empty, never nil", func(t *testing.T) {
		remote := &fakeMembershipRemote{active: &models.RemoteActiveMemberships{Enforced: true, TokenIDs: nil}}
		svc := NewMembershipService(&logger, remote)

		_, tokens, err := svc.ActiveTokens(ctx, viewTenant)
		require.NoError(t, err)
		require.NotNil(t, tokens,
			"nil reaching the ANY() filter is the inversion this programme keeps paying for")
		assert.Empty(t, tokens)
	})

	// The rollout-order property: fleet-lite may deploy before the tenancy
	// release that adds the endpoint. A 404 means memberships do not exist on
	// that tenancy version, so no tenant can be enforced — unenforced, not an
	// outage. Anything else (503, timeout) stays fail-closed: unknown state
	// must never read as "show everything".
	t.Run("a remote 404 reads as unenforced, not as an outage", func(t *testing.T) {
		remote := &fakeMembershipRemote{err: &gateway.TenancyError{StatusCode: 404, Message: "not found"}}
		svc := NewMembershipService(&logger, remote)

		enforced, tokens, err := svc.ActiveTokens(ctx, viewTenant)
		require.NoError(t, err)
		assert.False(t, enforced)
		require.NotNil(t, tokens)
		assert.Empty(t, tokens)

		// And the answer is cached like any other success — otherwise every
		// vehicle-list request hammers the missing endpoint until the release.
		_, _, err = svc.ActiveTokens(ctx, viewTenant)
		require.NoError(t, err)
		assert.Equal(t, 1, remote.calls)
	})

	t.Run("a remote 503 still fails closed", func(t *testing.T) {
		remote := &fakeMembershipRemote{err: &gateway.TenancyError{StatusCode: 503, Message: "unavailable"}}
		svc := NewMembershipService(&logger, remote)
		_, _, err := svc.ActiveTokens(ctx, viewTenant)
		assert.ErrorIs(t, err, ErrMembershipScopeUnavailable)
	})

	t.Run("no remote configured is the scope-unavailable sentinel", func(t *testing.T) {
		svc := NewMembershipService(&logger, nil)
		_, _, err := svc.ActiveTokens(ctx, viewTenant)
		assert.ErrorIs(t, err, ErrMembershipScopeUnavailable)
		assert.False(t, svc.Configured())
	})
}
