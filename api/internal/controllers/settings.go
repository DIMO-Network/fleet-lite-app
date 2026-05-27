package controllers

import (
	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

type SettingsController struct {
	settings *config.Settings
	logger   *zerolog.Logger
}

func NewSettingsController(settings *config.Settings, logger *zerolog.Logger) *SettingsController {
	return &SettingsController{settings: settings, logger: logger}
}

// PublicSettings is the subset of config exposed to the unauthenticated frontend.
// Used by the login button to construct the LIWD redirect URL.
type PublicSettings struct {
	ClientID string `json:"clientId"`
	LoginURL string `json:"loginUrl"`
	ChainID  int64  `json:"chainId"`
}

// GetPublicSettings — GET /public/settings (no auth).
func (s *SettingsController) GetPublicSettings(c *fiber.Ctx) error {
	return c.JSON(PublicSettings{
		ClientID: s.settings.DimoAuthClientID.Hex(),
		LoginURL: s.settings.DimoLoginURL.String(),
		ChainID:  s.settings.ChainID,
	})
}
