package controllers

import (
	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

type VehiclesController struct {
	settings *config.Settings
	logger   *zerolog.Logger
	identity gateway.IdentityAPI
}

func NewVehiclesController(settings *config.Settings, logger *zerolog.Logger, identity gateway.IdentityAPI) *VehiclesController {
	return &VehiclesController{
		settings: settings,
		logger:   logger,
		identity: identity,
	}
}

// GetVehicles — list every vehicle owned by the JWT-bearing wallet.
// GET /vehicles
func (v *VehiclesController) GetVehicles(c *fiber.Ctx) error {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}

	vehicles, err := v.identity.FetchVehiclesByWalletAddress(wallet.String())
	if err != nil {
		v.logger.Err(err).Str("wallet", wallet.String()).Msg("failed to fetch vehicles by wallet")
		return fiber.NewError(fiber.StatusBadGateway, "failed to fetch vehicles")
	}
	return c.JSON(fiber.Map{"vehicles": vehicles})
}

// GetVehicle — fetch a single vehicle by tokenID. Ownership is not enforced
// here; identity-api data is public. Add a JWT-vs-owner check later if/when
// we expose more sensitive vehicle data through this route.
// GET /vehicles/:tokenID
func (v *VehiclesController) GetVehicle(c *fiber.Ctx) error {
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	vehicle, err := v.identity.FetchVehicleByTokenID(int64(tokenID))
	if err != nil {
		v.logger.Err(err).Uint64("tokenID", tokenID).Msg("failed to fetch vehicle by tokenID")
		return fiber.NewError(fiber.StatusBadGateway, "failed to fetch vehicle")
	}
	return c.JSON(vehicle)
}
