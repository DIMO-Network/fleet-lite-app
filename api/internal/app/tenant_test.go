package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/controllers"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Which answers become a session, and with what scope. Every branch here is one
// somebody could otherwise get wrong silently, so each is pinned.
func TestDecideTenancyAccess(t *testing.T) {
	for _, tc := range []struct {
		name        string
		res         gateway.AuthzResult
		wantAllowed bool
		wantRole    string
		wantLimited bool
		wantGroups  []string
	}{
		{
			name:        "direct member becomes a session",
			res:         gateway.AuthzResult{Via: "direct", Member: true, Role: "owner"},
			wantAllowed: true,
			wantRole:    "owner",
		},
		{
			// Operator staff are b2b-only. A delegation is management access,
			// never a fleet-lite session, and never impersonation.
			name:        "delegated access is refused",
			res:         gateway.AuthzResult{Via: "delegation", Member: false, OperatorTenantID: "op-1"},
			wantAllowed: false,
		},
		{
			name:        "no access is refused",
			res:         gateway.AuthzResult{Via: "none", Member: false},
			wantAllowed: false,
		},
		{
			// Should not happen, but if the service ever says it, it must not
			// become a session.
			name:        "direct but not a member is refused",
			res:         gateway.AuthzResult{Via: "direct", Member: false},
			wantAllowed: false,
		},
		{
			name:        "nil scope is unrestricted",
			res:         gateway.AuthzResult{Via: "direct", Member: true, Role: "member", ScopeGroupIDs: nil},
			wantAllowed: true,
			wantRole:    "member",
			wantLimited: false,
		},
		{
			// The dangerous one. An empty scope means restricted to nothing; if
			// it is not recorded, GetAllowedGroups reads unrestricted and hands
			// them the whole fleet.
			name:        "empty scope restricts to nothing",
			res:         gateway.AuthzResult{Via: "direct", Member: true, Role: "member", ScopeGroupIDs: []string{}},
			wantAllowed: true,
			wantRole:    "member",
			wantLimited: true,
			wantGroups:  []string{},
		},
		{
			name:        "populated scope restricts to those groups",
			res:         gateway.AuthzResult{Via: "direct", Member: true, Role: "member", ScopeGroupIDs: []string{"t_a", "t_b"}},
			wantAllowed: true,
			wantRole:    "member",
			wantLimited: true,
			wantGroups:  []string{"t_a", "t_b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decideTenancyAccess(&tc.res)
			assert.Equal(t, tc.wantAllowed, got.Allowed)
			if !tc.wantAllowed {
				return
			}
			assert.Equal(t, tc.wantRole, got.Role)
			assert.Equal(t, tc.wantLimited, got.Limited)
			if tc.wantLimited {
				assert.Equal(t, tc.wantGroups, got.ScopeGroups)
			}
		})
	}
}

// A refused delegation is distinguishable from ordinary non-membership, so it
// can be logged as what it is: an operator trying to open a customer session.
func TestDecideTenancyAccessFlagsDelegation(t *testing.T) {
	d := decideTenancyAccess(&gateway.AuthzResult{Via: "delegation"})
	assert.False(t, d.Allowed)
	assert.True(t, d.Delegated)

	d = decideTenancyAccess(&gateway.AuthzResult{Via: "none"})
	assert.False(t, d.Allowed)
	assert.False(t, d.Delegated, "plain non-membership is not a delegation")
}

// The locals contract handlers depend on: presence of the entry is what marks a
// caller limited, which is why an empty scope must still be written.
func TestAllowedGroupsLocalsContract(t *testing.T) {
	for _, tc := range []struct {
		name        string
		decision    tenancyDecision
		wantLimited bool
		wantIDs     []string
	}{
		{
			name:        "unrestricted sets no entry",
			decision:    tenancyDecision{Allowed: true, Role: "owner"},
			wantLimited: false,
		},
		{
			name:        "restricted to nothing still sets the entry",
			decision:    tenancyDecision{Allowed: true, Role: "member", Limited: true, ScopeGroups: []string{}},
			wantLimited: true,
			wantIDs:     []string{},
		},
		{
			name:        "restricted to groups carries them",
			decision:    tenancyDecision{Allowed: true, Role: "member", Limited: true, ScopeGroups: []string{"t_a"}},
			wantLimited: true,
			wantIDs:     []string{"t_a"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotIDs []string
			var gotLimited bool

			app := fiber.New()
			app.Get("/probe", func(c *fiber.Ctx) error {
				// Exactly what authorizeViaTenancy does with the decision.
				c.Locals(controllers.RoleLocalsKey, tc.decision.Role)
				if tc.decision.Limited {
					c.Locals(controllers.AllowedGroupsLocalsKey, tc.decision.ScopeGroups)
				}
				gotIDs, gotLimited = controllers.GetAllowedGroups(c)
				return c.SendStatus(fiber.StatusOK)
			})

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/probe", nil))
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			assert.Equal(t, tc.wantLimited, gotLimited)
			if tc.wantLimited {
				assert.Equal(t, tc.wantIDs, gotIDs)
			}
		})
	}
}
