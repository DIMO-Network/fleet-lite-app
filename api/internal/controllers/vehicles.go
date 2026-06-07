package controllers

import (
	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

type VehiclesController struct {
	settings   *config.Settings
	logger     *zerolog.Logger
	vehicleSvc *service.VehicleService
}

func NewVehiclesController(settings *config.Settings, logger *zerolog.Logger, vehicleSvc *service.VehicleService) *VehiclesController {
	return &VehiclesController{
		settings:   settings,
		logger:     logger,
		vehicleSvc: vehicleSvc,
	}
}

// GetVehicles — list the current tenant's synced vehicles.
// GET /vehicles  (requires JWT + Tenant-Id)
func (v *VehiclesController) GetVehicles(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	vehicles, err := v.vehicleSvc.ListVehicles(c.Context(), tenant.ID)
	if err != nil {
		v.logger.Err(err).Str("tenant", tenant.ID).Msg("failed to list tenant vehicles")
		return fiber.NewError(fiber.StatusBadGateway, "failed to fetch vehicles")
	}
	return c.JSON(fiber.Map{"vehicles": vehicles})
}

// GetVehicle — fetch a single synced vehicle by tokenID, scoped to the tenant.
// GET /vehicles/:tokenID
func (v *VehiclesController) GetVehicle(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	vehicle, err := v.vehicleSvc.GetVehicle(c.Context(), tenant.ID, int64(tokenID))
	if err != nil {
		v.logger.Err(err).Uint64("tokenID", tokenID).Str("tenant", tenant.ID).Msg("failed to fetch vehicle")
		return fiber.NewError(fiber.StatusNotFound, "vehicle not found")
	}
	return c.JSON(vehicle)
}

// AddFavorite — star a vehicle for the current tenant ("account"). Favorites
// are shared across the tenant's members and pinned to the top of the list.
// POST /vehicles/:tokenID/favorite
func (v *VehiclesController) AddFavorite(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	if err := v.vehicleSvc.AddFavorite(c.Context(), tenant.ID, int64(tokenID)); err != nil {
		v.logger.Err(err).Uint64("tokenID", tokenID).Str("tenant", tenant.ID).Msg("failed to add favorite")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to add favorite")
	}
	return c.JSON(fiber.Map{"isFavorite": true})
}

// RemoveFavorite — unstar a vehicle for the current tenant.
// DELETE /vehicles/:tokenID/favorite
func (v *VehiclesController) RemoveFavorite(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	if err := v.vehicleSvc.RemoveFavorite(c.Context(), tenant.ID, int64(tokenID)); err != nil {
		v.logger.Err(err).Uint64("tokenID", tokenID).Str("tenant", tenant.ID).Msg("failed to remove favorite")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to remove favorite")
	}
	return c.JSON(fiber.Map{"isFavorite": false})
}
