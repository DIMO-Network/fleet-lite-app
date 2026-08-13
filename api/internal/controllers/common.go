package controllers

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

// TenantLocalsKey is where the tenant middleware stashes the resolved,
// decrypted tenant for the current request.
const TenantLocalsKey = "tenant"

// RoleLocalsKey is where the tenant middleware stashes the caller's membership
// role ("owner" / "member") for the current tenant.
const RoleLocalsKey = "tenant_role"

// AllowedGroupsLocalsKey is where the tenant middleware stashes the caller's
// allowed fleet-group ids — set ONLY when the caller is a limited member
// (owners and full-access members have no entry). See docs/GROUP_ACCESS_PLAN.md.
const AllowedGroupsLocalsKey = "tenant_allowed_groups"

// GetTenantRole returns the caller's role in the current tenant ("" if the
// tenant middleware didn't run).
func GetTenantRole(c *fiber.Ctx) string {
	role, _ := c.Locals(RoleLocalsKey).(string)
	return role
}

// GetAllowedGroups returns the fleet-group ids the caller is limited to.
// limited=false (ids nil) means unrestricted — an owner or a full-access
// member. limited=true with an empty slice is a member with access to nothing.
func GetAllowedGroups(c *fiber.Ctx) (ids []string, limited bool) {
	ids, limited = c.Locals(AllowedGroupsLocalsKey).([]string)
	return ids, limited
}

// ScopeUnavailable maps a failure to resolve a limited member's fleet groups
// into a 503, or returns nil if err is something else.
//
// 503 and not 403/404/500: the group structure comes from fleet-tenancy-api and
// this mirrors NewTenantMiddleware's treatment of an unavailable authz answer —
// our failure, not the caller's. Never recover by dropping the scope filter; an
// unscoped answer here is the whole fleet.
func ScopeUnavailable(err error) error {
	if errors.Is(err, service.ErrGroupScopeUnavailable) {
		return fiber.NewError(fiber.StatusServiceUnavailable, "authorization service unavailable")
	}
	return nil
}

// toSet builds a membership set from a slice of ids.
func toSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// RequireFullAccess rejects limited members. Management mutations (fleet-group
// and geofence CRUD) are reserved for owners and full-access members.
func RequireFullAccess(c *fiber.Ctx) error {
	if _, limited := GetAllowedGroups(c); limited {
		return fiber.NewError(fiber.StatusForbidden, "your access is limited to specific groups; ask an owner to make this change")
	}
	return nil
}

// GetTenant returns the resolved tenant the tenant middleware loaded into the
// request context (after validating the caller's membership).
func GetTenant(c *fiber.Ctx) (models.Tenant, error) {
	t, ok := c.Locals(TenantLocalsKey).(models.Tenant)
	if !ok {
		return models.Tenant{}, fmt.Errorf("no tenant in context")
	}
	return t, nil
}

// GetWalletAddressFromJWT pulls the `ethereum_address` claim from the bearer
// JWT that the gofiber/contrib/jwt middleware stashed at c.Locals("user").
func GetWalletAddressFromJWT(c *fiber.Ctx) (common.Address, error) {
	user, ok := c.Locals("user").(*jwt.Token)
	if !ok {
		return common.Address{}, fmt.Errorf("missing JWT in context")
	}
	claims, ok := user.Claims.(jwt.MapClaims)
	if !ok {
		return common.Address{}, fmt.Errorf("invalid JWT claims")
	}
	address, ok := claims["ethereum_address"].(string)
	if !ok {
		return common.Address{}, fmt.Errorf("ethereum_address not found in claims")
	}
	return common.HexToAddress(address), nil
}

// ParseTokenIDParam pulls a uint64 tokenID out of a Fiber path parameter.
func ParseTokenIDParam(c *fiber.Ctx, paramName string) (uint64, error) {
	raw := c.Params(paramName)
	if raw == "" {
		return 0, fiber.NewError(fiber.StatusBadRequest, paramName+" is required")
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fiber.NewError(fiber.StatusBadRequest, "invalid "+paramName+" format")
	}
	return id, nil
}
