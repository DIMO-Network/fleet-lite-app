package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const appClientID = "0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB"

// audienceApp stands in for jwtware: it puts a verified token with the given
// claims where jwtware would, then runs the audience check.
func audienceApp(claims jwt.MapClaims) *fiber.App {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if claims != nil {
			c.Locals("user", &jwt.Token{Claims: claims, Valid: true})
		}
		return c.Next()
	})
	app.Use(RequireTokenAudience(common.HexToAddress(appClientID)))
	app.Get("/vehicles", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	return app
}

func TestRequireTokenAudience(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims jwt.MapClaims
		want   int
	}{
		{
			// The shape auth.dimo.zone actually issues for this app's license.
			name:   "this app's token passes",
			claims: jwt.MapClaims{"aud": appClientID, "ethereum_address": "0xDE9651e2b884f19e7F4a45F23387114c3D5757EE"},
			want:   fiber.StatusOK,
		},
		{
			name:   "casing of the client id does not matter",
			claims: jwt.MapClaims{"aud": "0x51daCC165F1306ABFBF0A6312EC96E13AAA826DB"},
			want:   fiber.StatusOK,
		},
		{
			name:   "an audience list containing this app passes",
			claims: jwt.MapClaims{"aud": []any{"login-with-dimo", appClientID}},
			want:   fiber.StatusOK,
		},
		{
			// A fleet's own license coming back from "Grant permissions", or
			// any third-party DIMO app: valid signature, wrong app.
			name:   "a token issued to another license is rejected",
			claims: jwt.MapClaims{"aud": "0x1111111111111111111111111111111111111111"},
			want:   fiber.StatusUnauthorized,
		},
		{
			name:   "DIMO's own social-login client is rejected",
			claims: jwt.MapClaims{"aud": "login-with-dimo"},
			want:   fiber.StatusUnauthorized,
		},
		{
			name:   "a token with no audience is rejected",
			claims: jwt.MapClaims{"ethereum_address": "0xDE9651e2b884f19e7F4a45F23387114c3D5757EE"},
			want:   fiber.StatusUnauthorized,
		},
		{
			name:   "a malformed audience is rejected",
			claims: jwt.MapClaims{"aud": 42},
			want:   fiber.StatusUnauthorized,
		},
		{
			name:   "no token at all is rejected",
			claims: nil,
			want:   fiber.StatusUnauthorized,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := audienceApp(tc.claims).Test(httptest.NewRequest(http.MethodGet, "/vehicles", nil))
			require.NoError(t, err)
			assert.Equal(t, tc.want, resp.StatusCode)
		})
	}
}
