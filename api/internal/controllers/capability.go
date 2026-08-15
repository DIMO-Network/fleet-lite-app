package controllers

import (
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// Capabilities this app checks — the Q5 subset of the shared model. The other
// two (onboard_vehicles, reports) belong to the oracle; fleet-lite never gates
// on them.
const (
	CapManageMembers  = "manage_members"
	CapManageSettings = "manage_settings"
)

// requireTenantCapability resolves the caller's wallet and confirms they hold
// the capability in the :id path tenant.
//
// This replaces the role == owner gates: permissions[] is authoritative and
// role is a label, so the check reads the tenancy authz answer — which is also
// the only place an operator-managed tenant's memberships exist. A managed
// tenant's admin holding manage_members can manage members here without ever
// carrying the "owner" label.
//
// The local owner role stands in only when no tenancy client is configured
// (local dev): owners hold every capability, everyone else holds none, which
// is exactly what the old gates enforced.
func requireTenantCapability(
	c *fiber.Ctx,
	logger *zerolog.Logger,
	tenantSvc *service.TenantService,
	tenancy *gateway.TenancyAPI,
	capability string,
) (string, error) {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return "", fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	tenantID := c.Params("id")
	if tenantID == "" {
		return "", fiber.NewError(fiber.StatusBadRequest, "tenant id is required")
	}

	if tenancy == nil || !tenancy.Configured() {
		role, rerr := tenantSvc.GetMembershipRole(c.Context(), tenantID, wallet.Hex())
		if rerr != nil || role == "" {
			return "", fiber.NewError(fiber.StatusForbidden, "no access to tenant")
		}
		if role != service.RoleOwner {
			return "", fiber.NewError(fiber.StatusForbidden, "you need the "+capability+" capability")
		}
		return wallet.Hex(), nil
	}

	tenant, terr := tenantSvc.GetOrMirrorTenant(c.Context(), tenantID)
	if terr != nil {
		return "", fiber.NewError(fiber.StatusForbidden, "no access to tenant")
	}
	res, aerr := tenancy.Authz(c.Context(), *tenant, wallet.Hex())
	if aerr != nil {
		// Fail closed, as unavailability: a dependency failure is not an
		// authorization decision.
		logger.Err(aerr).Str("tenant_id", tenantID).Str("wallet", wallet.Hex()).
			Msg("tenancy capability check unavailable")
		return "", fiber.NewError(fiber.StatusServiceUnavailable, "authorization service unavailable")
	}
	if !res.Member || res.Via != "direct" {
		return "", fiber.NewError(fiber.StatusForbidden, "no access to tenant")
	}
	if !res.HasCapability(capability) {
		return "", fiber.NewError(fiber.StatusForbidden, "you need the "+capability+" capability")
	}
	return wallet.Hex(), nil
}
