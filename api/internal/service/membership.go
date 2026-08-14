package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog"
)

// ErrMembershipScopeUnavailable means the answer to "which vehicles are paid
// for" could not be fetched. Callers map it to 503 exactly like
// ErrGroupScopeUnavailable, and for the same reason: with enforcement on, an
// unfiltered list would show a customer vehicles their operator has switched
// off. "Tenancy is down so show everything" is the precise shape of the
// geofence count leak #115 fixed — never degrade to no filter.
var ErrMembershipScopeUnavailable = errors.New("membership scope is unavailable")

// membershipTTL bounds how stale a cached answer may be. Matches the authz and
// group-index windows: all three gate the same request, and there is no sense
// in one being fresher than the others.
//
// Unlike the group index there is no write-path invalidation, because this app
// never writes memberships — the operator's console does, in another service
// entirely. The TTL is therefore the whole freshness story: a toggle flip or a
// new membership takes up to 60s to be visible here, which is the same lag the
// operator already accepts for authz changes.
const membershipTTL = 60 * time.Second

// remoteMembershipSource is the slice of gateway.TenancyAPI this service needs.
// An interface so the filter is testable without a live tenancy client.
type remoteMembershipSource interface {
	ActiveVehicleMemberships(ctx context.Context, tenant models.Tenant) (*models.RemoteActiveMemberships, error)
	VehicleMemberships(ctx context.Context, tenant models.Tenant) (*models.RemoteMembershipList, error)
}

// MembershipService reads the commercial record — which vehicles are paid for
// — from fleet-tenancy-api, which owns it. This app never writes memberships;
// they are bought and administered through the operator's console.
type MembershipService struct {
	logger *zerolog.Logger
	remote remoteMembershipSource

	// activeCache holds one RemoteActiveMemberships per tenant id. The gate
	// read runs on every vehicle-list request once enforcement is on; uncached
	// it would take this app's whole request rate to tenancy.
	activeCache *cache.Cache
}

func NewMembershipService(logger *zerolog.Logger, remote remoteMembershipSource) *MembershipService {
	return &MembershipService{
		logger:      logger,
		remote:      remote,
		activeCache: cache.New(membershipTTL, 2*membershipTTL),
	}
}

// Configured reports whether a remote is wired. False means memberships do not
// exist as a feature on this deployment: the filter is a no-op and the page's
// read serves 404, which the frontend renders as "not switched on yet".
func (s *MembershipService) Configured() bool { return s.remote != nil }

// ActiveTokens returns whether enforcement is on and the token ids currently
// paid for, cached per tenant.
//
// Only successes are cached, exactly as the authz and group-index caches do
// it: a cached failure would extend an outage past its cause.
func (s *MembershipService) ActiveTokens(ctx context.Context, tenant models.Tenant) (bool, []int64, error) {
	if s.remote == nil {
		return false, nil, fmt.Errorf("%w: tenancy client is not configured", ErrMembershipScopeUnavailable)
	}
	if cached, found := s.activeCache.Get(tenant.ID); found {
		a := cached.(*models.RemoteActiveMemberships)
		return a.Enforced, a.TokenIDs, nil
	}
	a, err := s.remote.ActiveVehicleMemberships(ctx, tenant)
	if err != nil {
		// A 404 is not an outage: it means the tenancy service predates the
		// endpoint — a version in which memberships do not exist, so no tenant
		// can have enforcement on. Treating it as unenforced makes the rollout
		// order-independent instead of 503ing every vehicle list until the
		// tenancy release lands. EVERY OTHER FAILURE STILL FAILS CLOSED: a 503
		// or timeout means the current state is unknown, and unknown must
		// never read as "show everything".
		var terr *gateway.TenancyError
		if errors.As(err, &terr) && terr.StatusCode == 404 {
			s.logger.Warn().Str("tenant", tenant.ID).
				Msg("tenancy service has no active-memberships endpoint; treating as unenforced")
			a = &models.RemoteActiveMemberships{Enforced: false, TokenIDs: []int64{}}
		} else {
			return false, nil, fmt.Errorf("%w: load active memberships from tenancy: %w", ErrMembershipScopeUnavailable, err)
		}
	}
	if a.TokenIDs == nil {
		// The endpoint guarantees [] over null, but this service's callers
		// build an ANY() filter from the slice, and nil-means-unrestricted is
		// the inversion this programme keeps paying for. Pin it here too.
		a.TokenIDs = []int64{}
	}
	s.activeCache.Set(tenant.ID, a, cache.DefaultExpiration)
	return a.Enforced, a.TokenIDs, nil
}

// List is the display read for the memberships page. Screen-shaped and
// uncached — staleness here is a row a customer refreshes for, not an access
// decision.
func (s *MembershipService) List(ctx context.Context, tenant models.Tenant) (*models.RemoteMembershipList, error) {
	if s.remote == nil {
		return nil, fmt.Errorf("%w: tenancy client is not configured", ErrMembershipScopeUnavailable)
	}
	out, err := s.remote.VehicleMemberships(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("%w: load memberships from tenancy: %w", ErrMembershipScopeUnavailable, err)
	}
	if out.Memberships == nil {
		out.Memberships = []models.RemoteMembership{}
	}
	return out, nil
}
