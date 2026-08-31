package service

import (
	"context"
	"errors"
	"testing"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/aarondl/null/v8"
	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTenancySource implements tenancySource for set-resolution tests.
//
// detail/token are opt-in: unset, they keep the "not under test" answer the
// set-resolution tests rely on, so a test that cares about one read does not
// have to stub the others.
type fakeTenancySource struct {
	configured bool
	entitled   []int64
	err        error

	detail    *models.RemoteTenantDetail
	detailErr error

	token      *models.RemoteMintedToken
	tokenErr   error
	tokenCalls int
}

func (f *fakeTenancySource) Configured() bool { return f.configured }

func (f *fakeTenancySource) Entitlements(context.Context, models.Tenant) ([]models.RemoteEntitlement, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]models.RemoteEntitlement, 0, len(f.entitled))
	for _, id := range f.entitled {
		out = append(out, models.RemoteEntitlement{VehicleTokenID: id})
	}
	return out, nil
}

func (f *fakeTenancySource) TenantDetail(context.Context, string) (*models.RemoteTenantDetail, error) {
	if f.detailErr != nil {
		return nil, f.detailErr
	}
	if f.detail == nil {
		return nil, errors.New("not under test")
	}
	return f.detail, nil
}

func (f *fakeTenancySource) DimoToken(context.Context, string) (*models.RemoteMintedToken, error) {
	f.tokenCalls++
	if f.tokenErr != nil {
		return nil, f.tokenErr
	}
	if f.token == nil {
		return nil, errors.New("not under test")
	}
	return f.token, nil
}

// fakeGroupIndexSource implements groupIndexSource over a real GroupIndex built
// from a group→token map, so scope resolution is exercised through the same
// TokenIDsForGroups the production path uses rather than a reimplementation.
type fakeGroupIndexSource struct {
	byGroup map[string][]int64
	err     error
}

func (f *fakeGroupIndexSource) groupIndex(context.Context, models.Tenant) (*GroupIndex, error) {
	if f.err != nil {
		return nil, f.err
	}
	groups := make([]models.RemoteFleetGroup, 0, len(f.byGroup))
	for id, tokens := range f.byGroup {
		groups = append(groups, models.RemoteFleetGroup{ID: id, Name: id, TokenIDs: tokens})
	}
	return NewGroupIndex(groups), nil
}

func newSvc(t *fakeTenancySource, m membershipGate, g groupIndexSource) *VehicleService {
	l := zerolog.Nop()
	s := &VehicleService{logger: &l}
	if t != nil {
		s.tenancy = t
	}
	s.memberships = m
	s.groups = g
	return s
}

var explicitTenant = models.Tenant{ID: "t-1"} // no ClientID = operator-managed

func TestResolvesFromTenancy(t *testing.T) {
	ten := &fakeTenancySource{configured: true}

	assert.True(t, newSvc(ten, nil, nil).resolvesFromTenancy(explicitTenant),
		"credential-less tenant with a configured client resolves from tenancy")

	assert.False(t, newSvc(ten, nil, nil).resolvesFromTenancy(models.Tenant{ID: "t-2", ClientID: "0xabc"}),
		"a self-serve tenant's fleet IS its privileged set; the local table is correct for it")

	assert.False(t, newSvc(&fakeTenancySource{configured: false}, nil, nil).resolvesFromTenancy(explicitTenant),
		"an unconfigured client cannot resolve anything")

	assert.False(t, newSvc(nil, nil, nil).resolvesFromTenancy(explicitTenant),
		"no client at all")
}

// The incident's own numbers: nine entitled, all actively membered, no group
// scope. The old path intersected a nightly cache holding one revoked token
// against these live gates and returned nothing.
func TestResolveTokenSetIncidentShape(t *testing.T) {
	entitled := []int64{193196, 193491, 193492, 193493, 193494, 193498, 193499, 193552, 193556}
	s := newSvc(
		&fakeTenancySource{configured: true, entitled: entitled},
		&fakeMembershipGate{enforced: true, tokens: entitled},
		nil,
	)

	got, err := s.resolveTokenSet(context.Background(), explicitTenant, nil)
	require.NoError(t, err)
	assert.Equal(t, entitled, got, "sorted, and every entitled+membered vehicle present")
}

// Enforcement narrows the entitled set; it is not the set itself.
func TestResolveTokenSetMembershipGate(t *testing.T) {
	s := newSvc(
		&fakeTenancySource{configured: true, entitled: []int64{1, 2, 3}},
		&fakeMembershipGate{enforced: true, tokens: []int64{2, 3, 99}},
		nil,
	)

	got, err := s.resolveTokenSet(context.Background(), explicitTenant, nil)
	require.NoError(t, err)
	assert.Equal(t, []int64{2, 3}, got,
		"99 is membered but not entitled — memberships narrow, they never add")
}

// Enforcement off means the gate does not apply at all, not that it applies
// with an empty set.
func TestResolveTokenSetMembershipUnenforced(t *testing.T) {
	s := newSvc(
		&fakeTenancySource{configured: true, entitled: []int64{1, 2}},
		&fakeMembershipGate{enforced: false, tokens: nil},
		nil,
	)

	got, err := s.resolveTokenSet(context.Background(), explicitTenant, nil)
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2}, got)
}

// A tenant with enforcement on and nothing active has paid for nothing. This
// must be an empty fleet, not an unfiltered one.
func TestResolveTokenSetEnforcedWithNoActiveMemberships(t *testing.T) {
	s := newSvc(
		&fakeTenancySource{configured: true, entitled: []int64{1, 2, 3}},
		&fakeMembershipGate{enforced: true, tokens: nil},
		nil,
	)

	got, err := s.resolveTokenSet(context.Background(), explicitTenant, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestResolveTokenSetGroupScope(t *testing.T) {
	s := newSvc(
		&fakeTenancySource{configured: true, entitled: []int64{1, 2, 3, 4}},
		nil,
		&fakeGroupIndexSource{byGroup: map[string][]int64{
			"g-a": {2, 3},
			"g-b": {4, 77},
		}},
	)

	got, err := s.resolveTokenSet(context.Background(), explicitTenant, []string{"g-a"})
	require.NoError(t, err)
	assert.Equal(t, []int64{2, 3}, got)

	both, err := s.resolveTokenSet(context.Background(), explicitTenant, []string{"g-a", "g-b"})
	require.NoError(t, err)
	assert.Equal(t, []int64{2, 3, 4}, both, "77 is in scope but not entitled")
}

// A member scoped to nothing reaches nothing. There is no value of
// allowedGroupIDs that means "everything" — skipping the intersection on an
// empty list once handed a limited member the entire fleet.
func TestResolveTokenSetEmptyScopeReachesNothing(t *testing.T) {
	s := newSvc(
		&fakeTenancySource{configured: true, entitled: []int64{1, 2, 3}},
		nil,
		&fakeGroupIndexSource{byGroup: map[string][]int64{"g-a": {1, 2}}},
	)

	got, err := s.resolveTokenSet(context.Background(), explicitTenant, []string{})
	require.NoError(t, err)
	assert.Empty(t, got, "an empty allowed-group list must match zero vehicles")
}

// nil means unrestricted — the owner case — and must not be confused with the
// empty-slice case above.
func TestResolveTokenSetNilScopeIsUnrestricted(t *testing.T) {
	s := newSvc(
		&fakeTenancySource{configured: true, entitled: []int64{1, 2, 3}},
		nil,
		&fakeGroupIndexSource{byGroup: map[string][]int64{"g-a": {1}}},
	)

	got, err := s.resolveTokenSet(context.Background(), explicitTenant, nil)
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2, 3}, got)
}

// Every leg must fail closed. An unavailable gate can never degrade into an
// unfiltered set — that is how a scoped member would see somebody else's fleet.
func TestResolveTokenSetFailsClosed(t *testing.T) {
	boom := errors.New("tenancy unavailable")

	_, err := newSvc(&fakeTenancySource{configured: true, err: boom}, nil, nil).
		resolveTokenSet(context.Background(), explicitTenant, nil)
	require.Error(t, err, "entitlements unavailable")

	_, err = newSvc(
		&fakeTenancySource{configured: true, entitled: []int64{1}},
		&fakeMembershipGate{err: boom},
		nil,
	).resolveTokenSet(context.Background(), explicitTenant, nil)
	require.Error(t, err, "membership gate unavailable")

	_, err = newSvc(
		&fakeTenancySource{configured: true, entitled: []int64{1}},
		nil,
		&fakeGroupIndexSource{err: boom},
	).resolveTokenSet(context.Background(), explicitTenant, []string{"g-a"})
	require.Error(t, err, "group index unavailable")
}

// --- the LEFT-JOIN, which is the part plan 07 step 2 makes a gate ---

func vehicleRow(tokenID int64, brand, model string) *dbmodels.Vehicle {
	return &dbmodels.Vehicle{
		TokenID: tokenID,
		Make:    null.StringFrom(brand),
		Model:   null.StringFrom(model),
	}
}

// THE CASE THE PLAN NAMES: a token is entitled, membered and in scope, but the
// nightly sync has not cached metadata for it yet. It must still appear.
//
// This is the inversion that turns the incident's empty list into nine vehicles
// with thin metadata. An inner join would return a provably-correct set and a
// short response — the same bug, somewhere harder to see.
func TestMergeResolvedVehiclesMissingRow(t *testing.T) {
	resolved := []int64{1, 2, 3}
	rows := []*dbmodels.Vehicle{vehicleRow(2, "Toyota", "Hilux")}

	got := mergeResolvedVehicles(resolved, rows, nil)

	require.Len(t, got, 3, "every resolved token appears, cached or not")

	assert.Equal(t, int64(2), got[0].TokenID)
	assert.False(t, got[0].MetadataPending)
	assert.Equal(t, "Toyota", got[0].Definition.Make)

	assert.Equal(t, int64(1), got[1].TokenID)
	assert.True(t, got[1].MetadataPending, "entitled but uncached")
	assert.Equal(t, int64(3), got[2].TokenID)
	assert.True(t, got[2].MetadataPending)
}

// The whole fleet uncached — a customer entitled this minute, before any sync
// has run. The old path showed them nothing at all.
func TestMergeResolvedVehiclesAllMissing(t *testing.T) {
	got := mergeResolvedVehicles([]int64{9, 8, 7}, nil, nil)

	require.Len(t, got, 3)
	for _, v := range got {
		assert.True(t, v.MetadataPending)
	}
	assert.Equal(t, []int64{9, 8, 7}, []int64{got[0].TokenID, got[1].TokenID, got[2].TokenID},
		"thin rows keep the resolved order, which resolveTokenSet sorts")
}

// A fully-cached fleet must be byte-identical to what the old path produced —
// no MetadataPending anywhere, so the flag never appears on the wire for a
// healthy tenant.
func TestMergeResolvedVehiclesFullyCached(t *testing.T) {
	rows := []*dbmodels.Vehicle{vehicleRow(1, "Ford", "Ranger"), vehicleRow(2, "Toyota", "Hilux")}

	got := mergeResolvedVehicles([]int64{1, 2}, rows, nil)

	require.Len(t, got, 2)
	for _, v := range got {
		assert.False(t, v.MetadataPending)
	}
}

// A local row outside the resolved set is NOT returned. This is the revoked-
// entitlement direction: the operator took the vehicle away, the prune has not
// run yet, and the set — not the cache — is the authority.
func TestMergeResolvedVehiclesIgnoresUnresolvedRows(t *testing.T) {
	rows := []*dbmodels.Vehicle{vehicleRow(1, "Ford", "Ranger")}

	got := mergeResolvedVehicles([]int64{1}, rows, nil)
	require.Len(t, got, 1)
	assert.Equal(t, int64(1), got[0].TokenID)
}

// Favourites are keyed by token id and must survive onto a thin row — the star
// is app-local and does not depend on metadata having been cached.
func TestMergeResolvedVehiclesFavouriteOnThinRow(t *testing.T) {
	got := mergeResolvedVehicles([]int64{5}, nil, map[int64]bool{5: true})

	require.Len(t, got, 1)
	assert.True(t, got[0].MetadataPending)
	assert.True(t, got[0].IsFavorite)
}

// An empty resolved set is an empty fleet, not an unfiltered one.
func TestMergeResolvedVehiclesEmptySet(t *testing.T) {
	assert.Empty(t, mergeResolvedVehicles(nil, nil, nil))
}

// --- cache coherence ---

// countingTenancy counts Entitlements calls so the read-path cache is provable.
type countingTenancy struct {
	fakeTenancySource
	calls int
}

func (c *countingTenancy) Entitlements(ctx context.Context, t models.Tenant) ([]models.RemoteEntitlement, error) {
	c.calls++
	return c.fakeTenancySource.Entitlements(ctx, t)
}

// The set read runs on every fleet render. It must be cached at the same TTL as
// the membership gate and the group index — a live set against 60s-stale gates
// is the same mixing this path exists to remove, with the staleness on the
// other foot.
func TestEntitledTokenIDsCaches(t *testing.T) {
	ten := &countingTenancy{fakeTenancySource: fakeTenancySource{configured: true, entitled: []int64{1, 2}}}
	l := zerolog.Nop()
	s := &VehicleService{logger: &l, tenancy: ten, entitledCache: cache.New(entitledTTL, 2*entitledTTL)}

	for i := 0; i < 3; i++ {
		got, err := s.entitledTokenIDs(context.Background(), explicitTenant)
		require.NoError(t, err)
		assert.Equal(t, []int64{1, 2}, got)
	}
	assert.Equal(t, 1, ten.calls, "three renders, one upstream call")
}

// A failure must not be cached — that would extend an outage past its cause,
// the same rule the authz, membership and group-index caches follow.
func TestEntitledTokenIDsDoesNotCacheFailures(t *testing.T) {
	ten := &countingTenancy{fakeTenancySource: fakeTenancySource{configured: true, err: errors.New("down")}}
	l := zerolog.Nop()
	s := &VehicleService{logger: &l, tenancy: ten, entitledCache: cache.New(entitledTTL, 2*entitledTTL)}

	for i := 0; i < 2; i++ {
		_, err := s.entitledTokenIDs(context.Background(), explicitTenant)
		require.Error(t, err)
	}
	assert.Equal(t, 2, ten.calls, "each attempt retries upstream")
}

// The TTL must match the other two gates. If this ever diverges, the set and
// its gates age differently again.
func TestEntitledTTLMatchesMembershipTTL(t *testing.T) {
	assert.Equal(t, membershipTTL, entitledTTL,
		"the set and every gate over it must age together")
}
