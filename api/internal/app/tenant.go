package app

import (
	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/controllers"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// NewTenantMiddleware resolves and authorizes the current tenant for a request.
// It reads the `Tenant-Id` header, verifies the JWT wallet may act in that
// tenant, loads the tenant (with decrypted DIMO credentials), and stashes it at
// c.Locals(controllers.TenantLocalsKey) for downstream handlers.
//
// Two implementations, selected by Settings.TenancyAuthzEnabled:
//
//   - fleet-tenancy-api (`authorizeViaTenancy`) — the shared source of truth.
//   - this app's own tenant_users table (`authorizeLocally`) — what shipped
//     before, kept only so the flag can be turned back off.
//
// The flag exists to make the switch reversible without a rollback build. Once
// the tenancy path has run in production, delete the flag and the local path
// together — leaving both means two answers to one question, which is the
// condition the shared service exists to end.
func NewTenantMiddleware(
	tenantSvc *service.TenantService,
	tenancyAPI *gateway.TenancyAPI,
	settings *config.Settings,
	logger *zerolog.Logger,
) fiber.Handler {
	return func(c *fiber.Ctx) error {
		wallet, err := controllers.GetWalletAddressFromJWT(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, err.Error())
		}

		tenantID := c.Get("Tenant-Id")
		if tenantID == "" {
			return fiber.NewError(fiber.StatusBadRequest, "Tenant-Id header is required")
		}

		if settings.TenancyAuthzEnabled {
			return authorizeViaTenancy(c, tenantSvc, tenancyAPI, logger, tenantID, wallet)
		}
		return authorizeLocally(c, tenantSvc, logger, tenantID, wallet)
	}
}

// authorizeViaTenancy asks fleet-tenancy-api what this wallet may do here.
func authorizeViaTenancy(
	c *fiber.Ctx,
	tenantSvc *service.TenantService,
	tenancyAPI *gateway.TenancyAPI,
	logger *zerolog.Logger,
	tenantID string,
	wallet common.Address,
) error {
	// The tenant loads first here, where the local path loads it last. Asking
	// the tenancy service requires authenticating as the tenant being asked
	// about, so its credentials are needed before the question can be put.
	//
	// That means credentials are decrypted for a caller not yet known to be a
	// member. Nothing leaves the process — the tenancy client sends a minted
	// JWT, never the key — but it is work done on an unauthenticated caller's
	// behalf, so a bad Tenant-Id costs a decrypt. Acceptable; unavoidable
	// without a second, unauthenticated lookup path.
	tenant, err := tenantSvc.GetTenantByID(c.Context(), tenantID)
	if err != nil {
		logger.Err(err).Str("tenant_id", tenantID).Msg("load tenant")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load tenant")
	}

	res, err := tenancyAPI.Authz(c.Context(), *tenant, wallet.Hex())
	if err != nil {
		// Fail closed. Falling back to the local table on error would mean the
		// two sources silently swap under load, and every disagreement between
		// them would surface only during an incident — the worst moment to
		// discover one. 503 rather than 403: this is our failure, not theirs.
		logger.Err(err).Str("tenant_id", tenantID).Str("wallet", wallet.Hex()).
			Msg("tenancy authorization unavailable")
		return fiber.NewError(fiber.StatusServiceUnavailable, "authorization service unavailable")
	}

	decision := decideTenancyAccess(res)
	if !decision.Allowed {
		if decision.Delegated {
			logger.Warn().Str("tenant_id", tenantID).Str("wallet", wallet.Hex()).
				Msg("refusing delegated access to a fleet-lite session")
		}
		return fiber.NewError(fiber.StatusForbidden, "no access to tenant")
	}

	c.Locals(controllers.TenantLocalsKey, *tenant)
	c.Locals(controllers.RoleLocalsKey, decision.Role)
	if decision.Limited {
		c.Locals(controllers.AllowedGroupsLocalsKey, decision.ScopeGroups)
	}
	return c.Next()
}

// accessVia mirrors the tenancy service's models.AccessVia values.
type accessVia string

const (
	directVia     accessVia = "direct"
	delegationVia accessVia = "delegation"
)

// tenancyDecision is what an authz answer means for a fleet-lite session.
type tenancyDecision struct {
	Allowed bool
	Role    string
	// Limited mirrors the locals contract: presence of the allowed-groups entry
	// is what marks a caller restricted, so this says whether to set it at all.
	Limited bool
	// ScopeGroups is meaningful only when Limited. An empty, non-nil slice is a
	// caller restricted to nothing.
	ScopeGroups []string
	// Delegated records that the refusal was specifically a delegation, which
	// is worth a log line — it means an operator tried to open a customer
	// session, not that someone was simply not a member.
	Delegated bool
}

// decideTenancyAccess turns an authz answer into a session decision.
//
// Pure and separate from the middleware so the rules can be asserted without a
// database: this is authorization, and every branch here is one somebody could
// otherwise get wrong silently.
func decideTenancyAccess(res *gateway.AuthzResult) tenancyDecision {
	// A delegated answer is an operator holding management rights over this
	// tenant. It is deliberately not a fleet-lite session: operator staff work
	// in b2b and there is no impersonation here. Refusing it is the whole
	// reason Via is checked rather than Member alone.
	if res.Via == string(delegationVia) {
		return tenancyDecision{Delegated: true}
	}
	if !res.Member || res.Via != string(directVia) {
		return tenancyDecision{}
	}

	d := tenancyDecision{Allowed: true, Role: res.Role}
	// nil scope means unrestricted and gets no entry; an empty slice is a
	// member restricted to nothing, which must still be recorded — dropping it
	// would read as unrestricted and hand them the whole fleet.
	if !res.Unrestricted() {
		d.Limited = true
		d.ScopeGroups = res.ScopeGroupIDs
	}
	return d
}

// authorizeLocally is the pre-cutover path, reading this app's own
// tenant_users table. Delete it with the flag.
func authorizeLocally(
	c *fiber.Ctx,
	tenantSvc *service.TenantService,
	logger *zerolog.Logger,
	tenantID string,
	wallet common.Address,
) error {
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
