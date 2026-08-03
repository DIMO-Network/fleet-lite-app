package controllers

import (
	"errors"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

type InvitationsController struct {
	logger        *zerolog.Logger
	tenantSvc     *service.TenantService
	invitationSvc *service.InvitationService
}

func NewInvitationsController(
	logger *zerolog.Logger,
	tenantSvc *service.TenantService,
	invitationSvc *service.InvitationService,
) *InvitationsController {
	return &InvitationsController{
		logger:        logger,
		tenantSvc:     tenantSvc,
		invitationSvc: invitationSvc,
	}
}

// requireOwner resolves the caller's wallet, confirms membership in the path
// tenant, and that they're an owner. Mirrors TenantsController.requireMember.
func (ic *InvitationsController) requireOwner(c *fiber.Ctx) (string, error) {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return "", fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	tenantID := c.Params("id")
	if tenantID == "" {
		return "", fiber.NewError(fiber.StatusBadRequest, "tenant id is required")
	}
	role, err := ic.tenantSvc.GetMembershipRole(c.Context(), tenantID, wallet.Hex())
	if err != nil || role == "" {
		return "", fiber.NewError(fiber.StatusForbidden, "no access to tenant")
	}
	if role != service.RoleOwner {
		return "", fiber.NewError(fiber.StatusForbidden, "only an owner can manage invitations")
	}
	return wallet.Hex(), nil
}

type invitationJSON struct {
	ID         string  `json:"id"`
	Email      string  `json:"email"`
	Role       string  `json:"role"`
	Status     string  `json:"status"`
	InvitedBy  string  `json:"invitedBy,omitempty"`
	CreatedAt  string  `json:"createdAt"`
	ExpiresAt  string  `json:"expiresAt"`
	AcceptedAt *string `json:"acceptedAt,omitempty"`
	// AllowedGroupIds limits the future member to those fleet groups; absent =
	// full access. See docs/GROUP_ACCESS_PLAN.md.
	AllowedGroupIDs []string `json:"allowedGroupIds,omitempty"`
	// InviteeWallet is the wallet that accepted the invite — the account the
	// invitation actually bound to, which may differ from the emailed address's
	// expected owner (e.g. a shared session consumed the link).
	InviteeWallet *string `json:"inviteeWallet,omitempty"`
	// Email-delivery tracking, stamped on send and upgraded by the Postmark
	// webhook (see docs/POSTMARK_WEBHOOK_PLAN.md). EmailStatus is one of
	// sent | delivered | opened | bounced; absent means the email never
	// dispatched — the send failed, or sending is disabled (local dev).
	// Detail carries the bounce reason. Owner-only, like the rest of this shape.
	EmailStatus       *string `json:"emailStatus,omitempty"`
	EmailStatusAt     *string `json:"emailStatusAt,omitempty"`
	EmailStatusDetail *string `json:"emailStatusDetail,omitempty"`
	// EmailSent is set only on create/resend responses (true = the email
	// dispatched, false = saved but delivery failed). Omitted when listing.
	EmailSent *bool `json:"emailSent,omitempty"`
}

type createInvitationRequest struct {
	Email  string `json:"email"`
	Role   string `json:"role"`
	Locale string `json:"locale"`
	// AllowedGroupIds limits the invited member to those fleet groups; omit or
	// null for full access. Ignored for owner invites.
	AllowedGroupIDs []string `json:"allowedGroupIds"`
}

// CreateInvitation — POST /tenants/:id/invitations. Owner-only. Issues an
// email invitation and sends the accept-link via Postmark.
func (ic *InvitationsController) CreateInvitation(c *fiber.Ctx) error {
	inviter, err := ic.requireOwner(c)
	if err != nil {
		return err
	}
	var req createInvitationRequest
	if err := c.BodyParser(&req); err != nil || req.Email == "" {
		return fiber.NewError(fiber.StatusBadRequest, "email is required")
	}
	inv, err := ic.invitationSvc.Create(c.Context(), c.Params("id"), inviter, req.Email, req.Role, req.Locale, req.AllowedGroupIDs)
	if err != nil {
		// Saved-but-email-failed is a partial success: the invite row exists and is
		// usable, so return it (201) with emailSent=false instead of a 5xx that would
		// break the UI flow. The owner can resend once email delivery is fixed.
		if inv != nil && errors.Is(err, service.ErrEmailNotSent) {
			ic.logger.Warn().Err(err).Str("tenant", c.Params("id")).Msg("invitation created but email not sent")
			return c.Status(fiber.StatusCreated).JSON(toInvitationJSONWithEmail(inv, false))
		}
		// Nothing persisted — a real failure.
		ic.logger.Err(err).Str("tenant", c.Params("id")).Msg("create invitation")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to create invitation")
	}
	return c.Status(fiber.StatusCreated).JSON(toInvitationJSONWithEmail(inv, true))
}

// ListInvitations — GET /tenants/:id/invitations. Owner-only — invitations are
// a user-management surface (they carry emails and access scopes).
func (ic *InvitationsController) ListInvitations(c *fiber.Ctx) error {
	if _, err := ic.requireOwner(c); err != nil {
		return err
	}
	tenantID := c.Params("id")
	rows, err := ic.invitationSvc.List(c.Context(), tenantID)
	if err != nil {
		ic.logger.Err(err).Str("tenant", tenantID).Msg("list invitations")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list invitations")
	}
	out := make([]invitationJSON, 0, len(rows))
	for _, r := range rows {
		out = append(out, toInvitationJSON(r))
	}
	return c.JSON(fiber.Map{"invitations": out})
}

// RevokeInvitation — DELETE /tenants/:id/invitations/:invID. Owner-only.
func (ic *InvitationsController) RevokeInvitation(c *fiber.Ctx) error {
	if _, err := ic.requireOwner(c); err != nil {
		return err
	}
	if err := ic.invitationSvc.Revoke(c.Context(), c.Params("id"), c.Params("invID")); err != nil {
		ic.logger.Err(err).Str("tenant", c.Params("id")).Msg("revoke invitation")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to revoke invitation")
	}
	return c.JSON(fiber.Map{"ok": true})
}

type resendInvitationRequest struct {
	Locale string `json:"locale"`
}

// ResendInvitation — POST /tenants/:id/invitations/:invID/resend. Owner-only.
// Mints a fresh token and re-sends the email in the inviter's current locale.
func (ic *InvitationsController) ResendInvitation(c *fiber.Ctx) error {
	inviter, err := ic.requireOwner(c)
	if err != nil {
		return err
	}
	// Body is optional; an unparseable/empty body just falls back to English.
	var req resendInvitationRequest
	_ = c.BodyParser(&req)
	if err := ic.invitationSvc.Resend(c.Context(), c.Params("id"), c.Params("invID"), inviter, req.Locale); err != nil {
		if errors.Is(err, service.ErrInviteInvalid) {
			return fiber.NewError(fiber.StatusNotFound, "no pending invitation to resend")
		}
		// Token refreshed but email failed — partial success, warn instead of erroring.
		if errors.Is(err, service.ErrEmailNotSent) {
			ic.logger.Warn().Err(err).Str("tenant", c.Params("id")).Msg("invitation token refreshed but email not sent")
			return c.JSON(fiber.Map{"ok": true, "emailSent": false})
		}
		ic.logger.Err(err).Str("tenant", c.Params("id")).Msg("resend invitation")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to resend invitation")
	}
	return c.JSON(fiber.Map{"ok": true, "emailSent": true})
}

type acceptInvitationRequest struct {
	Token string `json:"token"`
}

// AcceptInvitation — POST /invitations/accept. JWT-authenticated but NOT
// membership-gated: the invitee is not yet a member. The token is the
// authorization; it resolves the tenant and binds the caller's JWT wallet.
func (ic *InvitationsController) AcceptInvitation(c *fiber.Ctx) error {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	var req acceptInvitationRequest
	if err := c.BodyParser(&req); err != nil || req.Token == "" {
		return fiber.NewError(fiber.StatusBadRequest, "token is required")
	}
	tenantID, err := ic.invitationSvc.Accept(c.Context(), req.Token, wallet.Hex())
	if err != nil {
		if errors.Is(err, service.ErrInviteInvalid) {
			return fiber.NewError(fiber.StatusGone, err.Error())
		}
		ic.logger.Err(err).Msg("accept invitation")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to accept invitation")
	}
	return c.JSON(fiber.Map{"ok": true, "tenantId": tenantID})
}

func toInvitationJSON(r *dbmodels.Invitation) invitationJSON {
	out := invitationJSON{
		ID:        r.ID,
		Email:     r.Email,
		Role:      r.Role,
		Status:    r.Status,
		InvitedBy: r.InvitedByWallet,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		ExpiresAt: r.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if r.AcceptedAt.Valid {
		s := r.AcceptedAt.Time.UTC().Format(time.RFC3339)
		out.AcceptedAt = &s
	}
	if r.InviteeWallet.Valid && r.InviteeWallet.String != "" {
		w := r.InviteeWallet.String
		out.InviteeWallet = &w
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
	out.AllowedGroupIDs = r.AllowedGroupIds
	return out
}

// toInvitationJSONWithEmail is toInvitationJSON plus the email-delivery flag, for
// create/resend responses where the caller needs to know if the email dispatched.
func toInvitationJSONWithEmail(r *dbmodels.Invitation, emailSent bool) invitationJSON {
	out := toInvitationJSON(r)
	out.EmailSent = &emailSent
	return out
}
