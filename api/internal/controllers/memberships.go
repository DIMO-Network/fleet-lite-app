package controllers

import (
	"errors"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// MembershipsController serves the read-only memberships page. This app never
// writes memberships — they are bought and administered through the operator's
// console — so there is exactly one route here.
type MembershipsController struct {
	logger      *zerolog.Logger
	memberships *service.MembershipService
}

func NewMembershipsController(logger *zerolog.Logger, memberships *service.MembershipService) *MembershipsController {
	return &MembershipsController{logger: logger, memberships: memberships}
}

// GetMemberships — GET /memberships (tenant-scoped).
//
// 404 when no tenancy client is configured, deliberately: the frontend's
// membership service treats 404 as "memberships are not switched on for this
// deployment" and renders that state, which is exactly right for an
// environment running without a tenancy service. A 5xx here would instead
// surface as an error the customer might report.
func (mc *MembershipsController) GetMemberships(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if mc.memberships == nil || !mc.memberships.Configured() {
		return fiber.NewError(fiber.StatusNotFound, "memberships are not available on this deployment")
	}
	out, lerr := mc.memberships.List(c.Context(), tenant)
	if lerr != nil {
		// A remote 404 means the tenancy service predates memberships — the
		// feature is absent, not broken. Surfaced as this app's own 404, which
		// the frontend renders as "not switched on yet".
		var terr *gateway.TenancyError
		if errors.As(lerr, &terr) && terr.StatusCode == fiber.StatusNotFound {
			return fiber.NewError(fiber.StatusNotFound, "memberships are not available on this deployment")
		}
		if serr := ScopeUnavailable(lerr); serr != nil {
			mc.logger.Err(lerr).Str("tenant", tenant.ID).Msg("memberships unavailable")
			return serr
		}
		mc.logger.Err(lerr).Str("tenant", tenant.ID).Msg("list memberships")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list memberships")
	}
	return c.JSON(out)
}
