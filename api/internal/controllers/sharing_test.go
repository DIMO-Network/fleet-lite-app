package controllers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedLicense string

func (f fixedLicense) EffectiveClientID(models.Tenant) string { return string(f) }

const fleetLicense = "0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB"

// sharingApp mounts the sharing routes behind a stand-in for the tenant
// middleware. There is no tenancy client, so a request that gets past the
// local checks ends at "sharing is not available" (503) — which is how these
// tests see which checks ran first.
func sharingApp(licenses LicenseResolver, wallet string) *fiber.App {
	logger := zerolog.Nop()
	ctrl := NewSharingController(&logger, nil, nil, nil, licenses)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals(TenantLocalsKey, models.Tenant{ID: "tenant-1"})
		if wallet != "" {
			c.Locals("user", &jwt.Token{Claims: jwt.MapClaims{"ethereum_address": wallet}})
		}
		return c.Next()
	})
	app.Post("/vehicles/:tokenID/share", ctrl.ShareVehicle)
	app.Delete("/vehicles/:tokenID/share/:grantee", ctrl.RevokeShare)
	app.Get("/vehicles/:tokenID/share/status", ctrl.ShareStatus)
	return app
}

func status(t *testing.T, app *fiber.App, method, path string) int {
	t.Helper()
	return statusWithBody(t, app, method, path, "")
}

func statusWithBody(t *testing.T, app *fiber.App, method, path, body string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := app.Test(req)
	require.NoError(t, err)
	return res.StatusCode
}

func TestRevokeShare_RefusesTheFleetsOwnLicense(t *testing.T) {
	const member = "0x264BC41755BA9F5a00DCEC07F96cB14339dBD970"
	tests := []struct {
		name     string
		licenses LicenseResolver
		grantee  string
		want     int
	}{
		{"the fleet's license", fixedLicense(fleetLicense), fleetLicense, fiber.StatusConflict},
		{"the fleet's license, lowercased", fixedLicense(fleetLicense), "0x51dacc165f1306abfbf0a6312ec96e13aaa826db", fiber.StatusConflict},
		{"another wallet goes on to the capability check", fixedLicense(fleetLicense), member, fiber.StatusServiceUnavailable},
		{"an unresolvable license matches nothing", fixedLicense(""), fleetLicense, fiber.StatusServiceUnavailable},
		{"no resolver matches nothing", nil, fleetLicense, fiber.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := sharingApp(tt.licenses, member)
			assert.Equal(t, tt.want, status(t, app, fiber.MethodDelete, "/vehicles/42/share/"+tt.grantee))
		})
	}
}

// Sharing again replaces a grant, so a share to the fleet's own license could
// shorten the fleet's access. Refused like a revoke.
func TestShareVehicle_RefusesTheFleetsOwnLicense(t *testing.T) {
	const member = "0x264BC41755BA9F5a00DCEC07F96cB14339dBD970"
	app := sharingApp(fixedLicense(fleetLicense), member)
	share := func(grantee string) int {
		return statusWithBody(t, app, fiber.MethodPost, "/vehicles/42/share",
			`{"grantee":"`+grantee+`","durationDays":30}`)
	}
	assert.Equal(t, fiber.StatusConflict, share(fleetLicense))
	assert.Equal(t, fiber.StatusConflict, share(strings.ToLower(fleetLicense)))
	assert.Equal(t, fiber.StatusServiceUnavailable, share(member), "anyone else goes on to the capability check")
}

// The status route used to go straight upstream: any member could read the
// outcome of any job id on any vehicle. It now checks the caller first.
func TestShareStatus_ChecksTheCallerBeforeGoingUpstream(t *testing.T) {
	const member = "0x264BC41755BA9F5a00DCEC07F96cB14339dBD970"
	path := "/vehicles/42/share/status?jobId=7"

	assert.Equal(t, fiber.StatusUnauthorized, status(t, sharingApp(nil, ""), fiber.MethodGet, path))
	assert.Equal(t, fiber.StatusServiceUnavailable, status(t, sharingApp(nil, member), fiber.MethodGet, path))
	assert.Equal(t, fiber.StatusBadRequest, status(t, sharingApp(nil, member), fiber.MethodGet, "/vehicles/42/share/status"))
}
