package app

import (
	"github.com/DIMO-Network/fleet-lite-app/internal/controllers"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// NewTenantMiddleware resolves and authorizes the current tenant for a request.
// It reads the `Tenant-Id` header, verifies the JWT wallet is a member of that
// tenant, loads the tenant (with decrypted DIMO credentials), and stashes it at
// c.Locals(controllers.TenantLocalsKey) for downstream handlers.
func NewTenantMiddleware(tenantSvc *service.TenantService, logger *zerolog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		wallet, err := controllers.GetWalletAddressFromJWT(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, err.Error())
		}

		tenantID := c.Get("Tenant-Id")
		if tenantID == "" {
			return fiber.NewError(fiber.StatusBadRequest, "Tenant-Id header is required")
		}

		membership, err := tenantSvc.GetMembership(c.Context(), tenantID, wallet.Hex())
		if err != nil || membership.Role == "" {
			return fiber.NewError(fiber.StatusForbidden, "no access to tenant")
		}

		tenant, err := tenantSvc.GetTenantByID(c.Context(), tenantID)
		if err != nil {
			logger.Err(err).Str("tenant_id", tenantID).Msg("load tenant")
			return fiber.NewError(fiber.StatusInternalServerError, "failed to load tenant")
		}

		c.Locals(controllers.TenantLocalsKey, *tenant)
		c.Locals(controllers.RoleLocalsKey, membership.Role)
		// Limited members carry their allowed group ids; owners and full-access
		// members (NULL column) get no entry = unrestricted. See GROUP_ACCESS_PLAN.md.
		if membership.Role != service.RoleOwner && membership.AllowedGroupIds != nil {
			c.Locals(controllers.AllowedGroupsLocalsKey, []string(membership.AllowedGroupIds))
		}
		return c.Next()
	}
}
