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
}

type createInvitationRequest struct {
	Email  string `json:"email"`
	Role   string `json:"role"`
	Locale string `json:"locale"`
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
	inv, err := ic.invitationSvc.Create(c.Context(), c.Params("id"), inviter, req.Email, req.Role, req.Locale)
	if err != nil {
		// A nil invite means it never persisted; a non-nil invite with an error
		// means it saved but the email failed — report that distinctly.
		if inv == nil {
			ic.logger.Err(err).Str("tenant", c.Params("id")).Msg("create invitation")
			return fiber.NewError(fiber.StatusInternalServerError, "failed to create invitation")
		}
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	return c.Status(fiber.StatusCreated).JSON(toInvitationJSON(inv))
}

// ListInvitations — GET /tenants/:id/invitations. Any member can list.
func (ic *InvitationsController) ListInvitations(c *fiber.Ctx) error {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	tenantID := c.Params("id")
	role, err := ic.tenantSvc.GetMembershipRole(c.Context(), tenantID, wallet.Hex())
	if err != nil || role == "" {
		return fiber.NewError(fiber.StatusForbidden, "no access to tenant")
	}
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
		ic.logger.Err(err).Str("tenant", c.Params("id")).Msg("resend invitation")
		return fiber.NewError(fiber.StatusBadGateway, "failed to resend invitation")
	}
	return c.JSON(fiber.Map{"ok": true})
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
	return out
}
