package service

import (
	"context"
	"strings"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog"
)

// shareableOwnersResolver is the slice of the tenancy client this service
// needs, narrowed so the annotation logic is testable without the client's
// unexported JWT provider.
type shareableOwnersResolver interface {
	Configured() bool
	ShareableOwners(ctx context.Context, tenant models.Tenant, owners []string) (shareable, unresolved []string, ownerModeWallet string, err error)
}

// SharingService decides which vehicles can be shared without their owner's
// passkey.
//
// The decision is not ours: the tenancy service owns the signer key and asks
// accounts-api whether the owner authorised it. We supply the owners because
// we already store one per vehicle and it holds only token ids.
type SharingService struct {
	logger  zerolog.Logger
	tenancy shareableOwnersResolver
}

func NewSharingService(logger *zerolog.Logger, tenancy shareableOwnersResolver) *SharingService {
	return &SharingService{
		logger:  logger.With().Str("component", "sharing").Logger(),
		tenancy: tenancy,
	}
}

// Configured reports whether the tenancy client exists. Sharing is off without
// it, which is the state of any environment running fleet-lite standalone.
func (s *SharingService) Configured() bool {
	return s.tenancy != nil && s.tenancy.Configured()
}

// AnnotateCanShare sets CanShare on each vehicle whose owner the tenant may
// sign for.
//
// BEST-EFFORT BY DESIGN. A failure leaves every CanShare false and the vehicle
// list is returned intact — the share button disappears, nothing else changes.
// That is the right trade for a display gate on a hot path: failing GET
// /vehicles because an accounts-api lookup was slow would blank the customer's
// whole fleet to hide one button. The share endpoint itself fails loudly, which
// is where a real problem needs to surface.
func (s *SharingService) AnnotateCanShare(ctx context.Context, tenant models.Tenant, vehicles []models.Vehicle) {
	if !s.Configured() || len(vehicles) == 0 {
		return
	}

	// Deduplicated before the call: a customer tenant's fleet usually sits on
	// one kernel account, so a hundred vehicles are typically one owner.
	seen := map[string]bool{}
	owners := make([]string, 0, 4)
	for _, v := range vehicles {
		if !common.IsHexAddress(v.Owner) {
			continue
		}
		key := common.HexToAddress(v.Owner).Hex()
		if !seen[key] {
			seen[key] = true
			owners = append(owners, key)
		}
	}
	if len(owners) == 0 {
		return
	}

	shareable, unresolved, ownerModeWallet, err := s.tenancy.ShareableOwners(ctx, tenant, owners)
	if err != nil {
		// Marked unknown, not silently unshareable: claiming "this owner has
		// not authorized sharing" when we never asked would send someone
		// chasing an authorization problem that is actually an outage.
		s.logger.Warn().Err(err).Str("tenant", tenant.ID).
			Msg("could not resolve shareable owners; reporting share status unknown")
		for i := range vehicles {
			vehicles[i].ShareBlocker = models.ShareBlockerUnknown
		}
		return
	}

	// Both sides are checksummed — the tenancy service returns EIP-55 and we
	// normalise before comparing — but EqualFold-by-map on the normalised form
	// keeps a non-checksummed write to owner_address from silently hiding the
	// button while every other owner check still passes.
	allowed := make(map[string]bool, len(shareable))
	for _, o := range shareable {
		allowed[strings.ToLower(o)] = true
	}
	// Owners the service has not determined yet. NOT the same as refused: on a
	// large fleet's first render most owners land here, and calling that "this
	// account hasn't authorized sharing" would be a confident answer to a
	// question nobody asked. Each render resolves more.
	pending := make(map[string]bool, len(unresolved))
	for _, o := range unresolved {
		pending[strings.ToLower(o)] = true
	}
	// With a fleet wallet configured, a refused owner gets the fleet-wallet
	// blocker instead of the signer one: the actionable fix for that tenant is
	// "this vehicle isn't held by the fleet wallet", not "ask the owner to
	// authorize sharing" — advice that would send the operator to the wrong
	// mechanism entirely.
	refused := models.ShareBlockerOwner
	if ownerModeWallet != "" {
		refused = models.ShareBlockerNotFleetWallet
	}
	for i := range vehicles {
		if !common.IsHexAddress(vehicles[i].Owner) {
			vehicles[i].ShareBlocker = models.ShareBlockerNoOwner
			continue
		}
		switch {
		case allowed[strings.ToLower(vehicles[i].Owner)]:
			vehicles[i].CanShare = true
		case pending[strings.ToLower(vehicles[i].Owner)]:
			vehicles[i].ShareBlocker = models.ShareBlockerUnknown
		default:
			vehicles[i].ShareBlocker = refused
		}
	}
}
