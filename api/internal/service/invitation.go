package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/rs/zerolog"
)

// InvitationService is a thin client over fleet-tenancy-api's invitation
// surface — P4 of that repo's docs/plans/04-invitations-into-tenancy.md.
//
// The whole lifecycle lives there: it mints the token, sends the email,
// receives Postmark's delivery webhooks, and writes the membership when
// someone accepts. This app no longer stores invitations, sends invitation
// email, or talks to Postmark at all.
//
// WHY THERE IS NO LOCAL FALLBACK, and why one must not be reintroduced: an
// invitation's token hash is what recognises an emailed link. Two services
// each holding a hash for one invitation means a link that works against one
// and not the other, and a resend against either silently kills the other's.
// That is why the cutover moved everything at once behind a single flag
// rather than splitting reads from writes the way the groups move did, and it
// is why nothing local survives here to fall back to. If the tenancy client
// is unconfigured, invitations are unavailable — deliberately, rather than
// quietly writing records nobody's accept path will ever consult.
type InvitationService struct {
	logger    *zerolog.Logger
	tenantSvc *TenantService
	tenancy   *gateway.TenancyAPI
}

func NewInvitationService(logger *zerolog.Logger, tenantSvc *TenantService) *InvitationService {
	return &InvitationService{logger: logger, tenantSvc: tenantSvc}
}

// UseTenancy wires the client every call goes through. Without it the service
// reports ErrInvitationsUnavailable rather than failing obscurely deeper in.
func (s *InvitationService) UseTenancy(client *gateway.TenancyAPI) { s.tenancy = client }

// ErrInviteInvalid is returned when an accept token does not match a usable
// (pending, unexpired) invitation. It is deliberately vague so callers can map
// it to a single user-facing message without leaking which check failed.
var ErrInviteInvalid = errors.New("invitation is invalid, already used, or expired")

// ErrEmailNotSent signals a partial success: the invitation record was written
// but the email failed to dispatch. Callers should treat this as
// success-with-warning, not a hard failure — the invite is usable and can be
// resent.
var ErrEmailNotSent = errors.New("invitation saved but the email could not be sent")

// ErrInvitationsUnavailable reports that the tenancy client is missing or
// unconfigured. Invitations have no local implementation to fall back to.
var ErrInvitationsUnavailable = errors.New("invitations are served by fleet-tenancy-api, which is not configured")

func (s *InvitationService) ready() error {
	if s.tenancy == nil || !s.tenancy.Configured() {
		return ErrInvitationsUnavailable
	}
	return nil
}

// Create issues an invitation. allowedGroupIDs keeps its nil-means-full-access
// meaning and is passed through as the tri-state the service demands: nil is
// unrestricted, an empty non-nil slice is access to nothing, and an absent
// field is refused upstream by design.
func (s *InvitationService) Create(ctx context.Context, tenantID, inviterWallet, email, role, locale string, allowedGroupIDs []string) (*models.RemoteInvitation, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	tenant, err := s.tenantSvc.GetOrMirrorTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load tenant for invitation: %w", err)
	}
	inv, err := s.tenancy.CreateInvitation(ctx, *tenant, gateway.RemoteInvitationCreate{
		Email:           email,
		Role:            role,
		Locale:          locale,
		ScopeGroupIDs:   allowedGroupIDs,
		InvitedByWallet: inviterWallet,
	})
	if err != nil {
		return nil, err
	}
	// The service answers 201 with emailSent=false when the record was written
	// but Postmark refused it. The record is authoritative and the email is
	// courtesy, so this is reported as partial success and the controller's
	// existing branch handles it.
	if inv.EmailSent != nil && !*inv.EmailSent {
		return inv, fmt.Errorf("%w: tenancy reported the invitation email was not dispatched", ErrEmailNotSent)
	}
	return inv, nil
}

// List returns a tenant's invitations, newest first.
func (s *InvitationService) List(ctx context.Context, tenantID string) ([]models.RemoteInvitation, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	tenant, err := s.tenantSvc.GetOrMirrorTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load tenant for invitations: %w", err)
	}
	return s.tenancy.Invitations(ctx, *tenant)
}

// Revoke marks a pending invitation revoked. Idempotent.
func (s *InvitationService) Revoke(ctx context.Context, tenantID, invitationID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	tenant, err := s.tenantSvc.GetOrMirrorTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("load tenant for invitation revoke: %w", err)
	}
	return s.tenancy.RevokeInvitation(ctx, *tenant, invitationID)
}

// Resend mints a fresh token and re-sends. The previous link dies — that is
// the contract, not a side effect.
func (s *InvitationService) Resend(ctx context.Context, tenantID, invitationID, inviterWallet, locale string) (*models.RemoteInvitation, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	tenant, err := s.tenantSvc.GetOrMirrorTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load tenant for invitation resend: %w", err)
	}
	inv, err := s.tenancy.ResendInvitation(ctx, *tenant, invitationID, inviterWallet, locale)
	if err != nil {
		// A dead or missing invitation is 404 there; the controller's existing
		// branch already maps ErrInviteInvalid to "no pending invitation".
		if httpStatus(err) == 404 {
			return nil, ErrInviteInvalid
		}
		return nil, err
	}
	if inv.EmailSent != nil && !*inv.EmailSent {
		return inv, fmt.Errorf("%w: tenancy reported the invitation email was not dispatched", ErrEmailNotSent)
	}
	return inv, nil
}

// Accept consumes a token and binds the wallet.
//
// THE GRANT IS NOT REPEATED HERE. fleet-tenancy-api's accept writes the
// membership and marks the invitation in ONE transaction — which is precisely
// the improvement over the retired local path's two steps — so granting
// afterwards would re-write what already exists, and would do it worse: a
// second write that can fail on its own leaves the caller reporting failure
// for an accept that fully succeeded.
func (s *InvitationService) Accept(ctx context.Context, token, inviteeWallet string) (*models.RemoteInvitation, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	inv, err := s.tenancy.AcceptInvitation(ctx, token, inviteeWallet, "")
	if err != nil {
		// 410 is the service's answer for every dead token — unknown,
		// superseded, used, expired, revoked — with nothing distinguishing
		// which, deliberately. It is exactly ErrInviteInvalid's contract.
		if st := httpStatus(err); st == 410 || st == 404 {
			s.logger.Info().Str("wallet", inviteeWallet).
				Msg("invite flow: accept refused by tenancy (dead, superseded or expired token)")
			return nil, ErrInviteInvalid
		}
		return nil, err
	}
	s.logger.Info().Str("invitation", inv.ID).Str("tenant", inv.TenantID).Str("email", inv.Email).
		Str("role", inv.Role).Str("inviteeWallet", inviteeWallet).
		Msg("invite flow: invitation accepted via tenancy (membership written there, in one transaction)")
	return inv, nil
}

// httpStatus extracts a status from a gateway error without importing its
// concrete type, matching how FleetGroupService reads TenancyError.
func httpStatus(err error) int {
	var he interface{ HTTPStatus() int }
	if errors.As(err, &he) {
		return he.HTTPStatus()
	}
	return 0
}
