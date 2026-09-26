package app

import (
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

// RequireTokenAudience rejects a DIMO JWT that was issued to a different app.
//
// jwtware checks that DIMO signed the token and that it has not expired. It
// does not check who the token was issued to. DIMO's auth server signs every
// Login-with-DIMO token with the same keys, whichever developer license asked
// for it, and records that license's client id in `aud` (verified against
// auth.dimo.zone: a web3 challenge for this app's license yields
// `"aud": "<DIMO_AUTH_CLIENT_ID>"`, checksummed). Without this check, a token
// minted for any other app authenticates here as its wallet: a third-party app
// a fleet admin once signed into, or a fleet's own license coming back from a
// "Grant permissions" round trip.
//
// Compared case-insensitively: the claim carries whatever casing the client id
// was requested with, and an address is the same address in either case.
func RequireTokenAudience(clientID common.Address) fiber.Handler {
	want := strings.ToLower(clientID.Hex())
	return func(c *fiber.Ctx) error {
		token, ok := c.Locals("user").(*jwt.Token)
		if !ok || token == nil {
			return fiber.NewError(fiber.StatusUnauthorized, "missing JWT")
		}
		audiences, err := token.Claims.GetAudience()
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "invalid JWT audience")
		}
		for _, aud := range audiences {
			if strings.ToLower(aud) == want {
				return c.Next()
			}
		}
		return fiber.NewError(fiber.StatusUnauthorized, "token was issued to a different app")
	}
}
