package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
)

// The invitation cutover — P2 of fleet-tenancy-api's
// docs/plans/04-invitations-into-tenancy.md.
//
// Behind INVITES_FROM_TENANCY the whole lifecycle moves: fleet-tenancy-api
// mints the token, sends the email, receives Postmark's delivery webhooks,
// and writes the membership when someone accepts. This file is the seam. The
// local implementations in invitation.go are untouched and remain the revert
// path until P4 deletes them.
//
// WHY THIS IS ALL-OR-NOTHING, unlike the groups move's read/write split: an
// invitation's token hash is what recognises an emailed link. Two services
// each holding a hash for one invitation means a link that works against one
// and not the other, and a resend against either silently kills the other's.
// So the flag moves everything at once, and the backfill copies the hashes
// first so links issued before the flip keep working after it.
//
// Both paths return models.RemoteInvitation. The local one converts on the way
// out rather than the controller branching on the flag — the wire shape the
// frontend sees must not depend on where the record lives, and when P4 deletes
// the local path there is nothing left to unpick.

// UseTenancy wires the tenancy client and whether invitations are served from
// it. A nil or unconfigured client keeps everything local regardless of the
// flag, which is what makes local development work with no tenancy service.
func (s *InvitationService) UseTenancy(client *gateway.TenancyAPI, fromTenancy bool) {
	s.tenancy = client
	s.fromTenancy = fromTenancy
}

// invitesFromTenancy reports whether this call should go to the shared model.
func (s *InvitationService) invitesFromTenancy() bool {
	return s.fromTenancy && s.tenancy != nil && s.tenancy.Configured()
}

// Create issues an invitation. allowedGroupIDs keeps its local nil-means-full
// -access meaning and is passed through as the tri-state the service demands.
func (s *InvitationService) Create(ctx context.Context, tenantID, inviterWallet, email, role, locale string, allowedGroupIDs []string) (*models.RemoteInvitation, error) {
	if !s.invitesFromTenancy() {
		inv, err := s.createLocal(ctx, tenantID, inviterWallet, email, role, locale, allowedGroupIDs)
		return localInvitationToRemote(inv), err
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
	// but Postmark refused it. That is the same partial success the local path
	// signals with ErrEmailNotSent, so it is reported the same way and the
	// controller's existing branch handles both.
	if inv.EmailSent != nil && !*inv.EmailSent {
		return inv, fmt.Errorf("%w: tenancy reported the invitation email was not dispatched", ErrEmailNotSent)
	}
	return inv, nil
}

// List returns a tenant's invitations, newest first.
func (s *InvitationService) List(ctx context.Context, tenantID string) ([]models.RemoteInvitation, error) {
	if !s.invitesFromTenancy() {
		rows, err := s.listLocal(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		out := make([]models.RemoteInvitation, 0, len(rows))
		for _, r := range rows {
			out = append(out, *localInvitationToRemote(r))
		}
		return out, nil
	}
	tenant, err := s.tenantSvc.GetOrMirrorTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load tenant for invitations: %w", err)
	}
	return s.tenancy.Invitations(ctx, *tenant)
}

// Revoke marks a pending invitation revoked. Idempotent on both paths.
func (s *InvitationService) Revoke(ctx context.Context, tenantID, invitationID string) error {
	if !s.invitesFromTenancy() {
		return s.revokeLocal(ctx, tenantID, invitationID)
	}
	tenant, err := s.tenantSvc.GetOrMirrorTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("load tenant for invitation revoke: %w", err)
	}
	return s.tenancy.RevokeInvitation(ctx, *tenant, invitationID)
}

// Resend mints a fresh token and re-sends. The previous link dies on either
// path — that is the contract, not a side effect.
func (s *InvitationService) Resend(ctx context.Context, tenantID, invitationID, inviterWallet, locale string) (*models.RemoteInvitation, error) {
	if !s.invitesFromTenancy() {
		inv, err := s.resendLocal(ctx, tenantID, invitationID, inviterWallet, locale)
		return localInvitationToRemote(inv), err
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
// THE GRANT IS NOT REPEATED HERE when the flag is on. fleet-tenancy-api's
// accept writes the membership and marks the invitation in ONE transaction —
// which is precisely the improvement over the local path's two steps — so
// calling GrantMember afterwards would re-write what already exists, and would
// do it worse: a second write that can fail on its own leaves the caller
// reporting failure for an accept that fully succeeded.
//
// Nothing local is written either. The local row, if the backfill copied one,
// is inert; the diff is what proves the two agreed before the flip, and P4
// drops the table.
func (s *InvitationService) Accept(ctx context.Context, token, inviteeWallet string) (*models.RemoteInvitation, error) {
	if !s.invitesFromTenancy() {
		inv, err := s.acceptLocal(ctx, token, inviteeWallet)
		return localInvitationToRemote(inv), err
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

// localInvitationToRemote converts a local row to the shared wire shape so both
// paths answer identically. nil in, nil out — the callers pass a possibly-nil
// row straight through alongside a partial-success error.
//
// allowed_group_ids becomes scopeGroupIds and the tri-state is preserved:
// nil stays nil (full access), an empty non-nil array stays empty (access to
// nothing). Collapsing those is the inversion that has bitten this programme
// repeatedly.
func localInvitationToRemote(r *dbmodels.Invitation) *models.RemoteInvitation {
	if r == nil {
		return nil
	}
	out := &models.RemoteInvitation{
		ID:        r.ID,
		TenantID:  r.TenantID,
		Email:     r.Email,
		Role:      r.Role,
		Status:    r.Status,
		InvitedBy: r.InvitedByWallet,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		ExpiresAt: r.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if r.AllowedGroupIds != nil {
		out.ScopeGroupIDs = []string(r.AllowedGroupIds)
	}
	if r.InviteeWallet.Valid && r.InviteeWallet.String != "" {
		w := r.InviteeWallet.String
		out.InviteeWallet = &w
	}
	if r.AcceptedAt.Valid {
		s := r.AcceptedAt.Time.UTC().Format(time.RFC3339)
		out.AcceptedAt = &s
	}
	if r.EmailStatus.Valid && r.EmailStatus.String != "" {
		s := r.EmailStatus.String
		out.EmailStatus = &s
	}
	if r.EmailStatusAt.Valid {
		s := r.EmailStatusAt.Time.UTC().Format(time.RFC3339)
		out.EmailStatusAt = &s
	}
	if r.EmailStatusDetail.Valid && r.EmailStatusDetail.String != "" {
		s := r.EmailStatusDetail.String
		out.EmailStatusDetail = &s
	}
	return out
}
