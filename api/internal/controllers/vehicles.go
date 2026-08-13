package controllers

import (
	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

type VehiclesController struct {
	settings   *config.Settings
	logger     *zerolog.Logger
	vehicleSvc *service.VehicleService
	groupSvc   *service.FleetGroupService
}

func NewVehiclesController(settings *config.Settings, logger *zerolog.Logger, vehicleSvc *service.VehicleService, groupSvc *service.FleetGroupService) *VehiclesController {
	return &VehiclesController{
		settings:   settings,
		logger:     logger,
		vehicleSvc: vehicleSvc,
		groupSvc:   groupSvc,
	}
}

// GetVehicles — list the current tenant's synced vehicles.
// GET /vehicles  (requires JWT + Tenant-Id)
func (v *VehiclesController) GetVehicles(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	allowed, limited := GetAllowedGroups(c)
	vehicles, err := v.vehicleSvc.ListVehicles(c.Context(), tenant, allowed)
	if err != nil {
		// A scope that could not be resolved fails the request. Returning the
		// list unscoped would hand a limited member the whole fleet.
		if serr := ScopeUnavailable(err); serr != nil {
			v.logger.Err(err).Str("tenant", tenant.ID).Msg("fleet group scope unavailable")
			return serr
		}
		v.logger.Err(err).Str("tenant", tenant.ID).Msg("failed to list tenant vehicles")
		return fiber.NewError(fiber.StatusBadGateway, "failed to fetch vehicles")
	}

	// Attach group membership for the fleet-overview map/list filter. Best-effort:
	// a failure here shouldn't blank the vehicle list. Each vehicle always carries
	// a (possibly empty) groups slice.
	groupsByToken, err := v.groupSvc.VehicleGroupsMapView(c.Context(), tenant)
	if err != nil {
		v.logger.Err(err).Str("tenant", tenant.ID).Msg("failed to load vehicle groups")
		groupsByToken = nil
	}
	allowedSet := toSet(allowed)
	for i := range vehicles {
		g := groupsByToken[vehicles[i].TokenID]
		// Limited members shouldn't see refs to groups outside their scope even
		// on vehicles they can access via another (allowed) group.
		if limited && g != nil {
			kept := g[:0:0]
			for _, ref := range g {
				if allowedSet[ref.ID] {
					kept = append(kept, ref)
				}
			}
			g = kept
		}
		if g != nil {
			vehicles[i].Groups = g
		} else {
			vehicles[i].Groups = []models.GroupRef{}
		}
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
	allowed, _ := GetAllowedGroups(c)
	vehicle, err := v.vehicleSvc.GetVehicle(c.Context(), tenant, int64(tokenID), allowed)
	if err != nil {
		// 503, not the usual 404: "we could not check" is not "it isn't there".
		if serr := ScopeUnavailable(err); serr != nil {
			v.logger.Err(err).Str("tenant", tenant.ID).Msg("fleet group scope unavailable")
			return serr
		}
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
	if allowed, limited := GetAllowedGroups(c); limited {
		if _, err := v.vehicleSvc.GetVehicle(c.Context(), tenant, int64(tokenID), allowed); err != nil {
			if serr := ScopeUnavailable(err); serr != nil {
				return serr
			}
			return fiber.NewError(fiber.StatusNotFound, "vehicle not found")
		}
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
	if allowed, limited := GetAllowedGroups(c); limited {
		if _, err := v.vehicleSvc.GetVehicle(c.Context(), tenant, int64(tokenID), allowed); err != nil {
			if serr := ScopeUnavailable(err); serr != nil {
				return serr
			}
			return fiber.NewError(fiber.StatusNotFound, "vehicle not found")
		}
	}
	if err := v.vehicleSvc.RemoveFavorite(c.Context(), tenant.ID, int64(tokenID)); err != nil {
		v.logger.Err(err).Uint64("tokenID", tokenID).Str("tenant", tenant.ID).Msg("failed to remove favorite")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to remove favorite")
	}
	return c.JSON(fiber.Map{"isFavorite": false})
}
