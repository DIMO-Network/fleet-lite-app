package app

import (
	"database/sql"
	"errors"

	"github.com/DIMO-Network/fleet-lite-app/internal/controllers"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// NewTenantMiddleware resolves and authorizes the current tenant for a request.
// It reads the `Tenant-Id` header, asks fleet-tenancy-api whether the JWT's
// wallet may act in that tenant, and stashes the resolved tenant at
// c.Locals(controllers.TenantLocalsKey) for downstream handlers.
//
// fleet-tenancy-api is the only source of this answer. This app's own
// tenant_users table is no longer consulted for authorization — it was read
// here until cutover, and both the flag and that path were removed once the
// tenancy path had run in production. Two answers to one question is the
// condition the shared service exists to end.
//
// tenant_users still exists and is still written (invitations, member
// management), and the backfill re-reads it; it simply no longer decides
// access. Anything that reintroduces a local authorization read here should
// instead be a capability in the shared model.
func NewTenantMiddleware(
	tenantSvc *service.TenantService,
	tenancyAPI *gateway.TenancyAPI,
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

		// The tenant loads before the authorization check because asking the
		// tenancy service requires authenticating, and for a self-serve tenant
		// that means its own credentials — so they are needed before the
		// question can be put. An operator-managed tenant has no local row at
		// all: GetOrMirrorTenant resolves it from the tenancy service and
		// writes the credential-less mirror row the local schema needs.
		//
		// That means credentials are decrypted for a caller not yet known to be
		// a member. Nothing leaves the process — the client sends a minted JWT,
		// never the key — but it is work done on an unauthenticated caller's
		// behalf, so a bad Tenant-Id costs a decrypt.
		tenant, err := tenantSvc.GetOrMirrorTenant(c.Context(), tenantID)
		if err != nil {
			// An unknown tenant is the caller naming something that does not
			// exist, not a fault here. 403 rather than 404 for the same reason
			// the tenancy service uses it: a 404 would let a caller probe which
			// tenant ids are real.
			if errors.Is(err, sql.ErrNoRows) {
				return fiber.NewError(fiber.StatusForbidden, "no access to tenant")
			}
			logger.Err(err).Str("tenant_id", tenantID).Msg("load tenant")
			return fiber.NewError(fiber.StatusInternalServerError, "failed to load tenant")
		}

		res, err := tenancyAPI.Authz(c.Context(), *tenant, wallet.Hex())
		if err != nil {
			// Fail closed. There is deliberately no local fallback: a second
			// source consulted only during an incident is a second source
			// nobody has verified. 503 rather than 403 — this is our failure,
			// not the caller's.
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
