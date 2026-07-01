package controllers

import (
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// UserPrefsController serves the caller's own UI preferences, keyed by the
// wallet in their JWT. Preferences are wallet-global (not tenant-scoped), so
// these endpoints live under the JWT-only group and take no tenant.
type UserPrefsController struct {
	logger *zerolog.Logger
	svc    *service.UserPrefsService
}

func NewUserPrefsController(logger *zerolog.Logger, svc *service.UserPrefsService) *UserPrefsController {
	return &UserPrefsController{logger: logger, svc: svc}
}

// allowedPrefs whitelists the preference keys and their accepted values. The DB
// stores an opaque blob, so this is the single guard keeping arbitrary
// client-supplied JSON out of it. Adding a preference = one entry here.
var allowedPrefs = map[string]map[string]bool{
	"units":         {"metric": true, "imperial": true},
	"locale":        {"en": true, "es": true},
	"tripMechanism": {"auto": true, "ignitionDetection": true, "frequencyAnalysis": true, "changePointDetection": true, "idling": true, "refuel": true, "recharge": true},
}

// sanitize drops unknown keys and out-of-range values, so only recognized
// preferences reach the DB.
func sanitize(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if values, ok := allowedPrefs[k]; ok && values[v] {
			out[k] = v
		}
	}
	return out
}

// GetPreferences — GET /me/preferences. Returns the caller's stored
// preferences ({} if none).
func (u *UserPrefsController) GetPreferences(c *fiber.Ctx) error {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	prefs, err := u.svc.Get(c.Context(), wallet.Hex())
	if err != nil {
		u.logger.Err(err).Str("wallet", wallet.Hex()).Msg("get user preferences")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load preferences")
	}
	return c.JSON(prefs)
}

// PutPreferences — PUT /me/preferences. Full-replaces the caller's preferences
// with the sanitized body (unknown keys / invalid values are dropped).
func (u *UserPrefsController) PutPreferences(c *fiber.Ctx) error {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	var body map[string]string
	if err := c.BodyParser(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid preferences body")
	}
	prefs := sanitize(body)
	if err := u.svc.Upsert(c.Context(), wallet.Hex(), prefs); err != nil {
		u.logger.Err(err).Str("wallet", wallet.Hex()).Msg("save user preferences")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to save preferences")
	}
	return c.JSON(prefs)
}
