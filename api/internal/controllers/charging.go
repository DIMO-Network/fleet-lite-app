// api/internal/controllers/charging.go
package controllers

import (
	"fmt"
	"strconv"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

type ChargingController struct {
	logger      *zerolog.Logger
	chargingSvc *service.ChargingService
	settingsSvc *service.ChargingSettingsService
	vehicleSvc  *service.VehicleService
}

func NewChargingController(logger *zerolog.Logger, chargingSvc *service.ChargingService, settingsSvc *service.ChargingSettingsService, vehicleSvc *service.VehicleService) *ChargingController {
	return &ChargingController{logger: logger, chargingSvc: chargingSvc, settingsSvc: settingsSvc, vehicleSvc: vehicleSvc}
}

// vehicleInTenant mirrors TCOController.vehicleInTenant exactly.
func (t *ChargingController) vehicleInTenant(c *fiber.Ctx, tenant models.Tenant, tokenID int64) error {
	allowed, _ := GetAllowedGroups(c)
	if _, err := t.vehicleSvc.GetVehicle(c.Context(), tenant, tokenID, allowed); err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}
	return nil
}

// parseWindow reads `from`/`to` query params (RFC3339), defaulting to the
// trailing 30 days when absent — charging tabs open to "recent activity",
// not an unbounded all-time scan.
func parseWindow(c *fiber.Ctx) (from, to time.Time, err error) {
	to = time.Now()
	from = to.AddDate(0, 0, -30)
	if q := c.Query("to"); q != "" {
		if to, err = time.Parse(time.RFC3339, q); err != nil {
			return from, to, fmt.Errorf("invalid to: %w", err)
		}
	}
	if q := c.Query("from"); q != "" {
		if from, err = time.Parse(time.RFC3339, q); err != nil {
			return from, to, fmt.Errorf("invalid from: %w", err)
		}
	}
	return from, to, nil
}

// GetSettings — GET /charging/settings.
func (t *ChargingController) GetSettings(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	settings, err := t.settingsSvc.GetSettings(c.Context(), tenant.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "get charging settings: "+err.Error())
	}
	return c.JSON(settings)
}

// PutSettings — PUT /charging/settings.
func (t *ChargingController) PutSettings(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	var req service.ChargingSettings
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body: "+err.Error())
	}
	if err := t.settingsSvc.UpsertSettings(c.Context(), tenant.ID, req); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "save charging settings: "+err.Error())
	}
	return c.JSON(req)
}

// GetSummary — GET /charging/summary?from&to. Fleet-wide rollup.
func (t *ChargingController) GetSummary(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	from, to, err := parseWindow(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	allowed, _ := GetAllowedGroups(c)
	summary, err := t.chargingSvc.FleetSummary(c.Context(), tenant, allowed, from, to)
	if err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		return fiber.NewError(fiber.StatusInternalServerError, "charging summary: "+err.Error())
	}
	return c.JSON(summary)
}

// GetVehicleSessions — GET /charging/:tokenId/sessions?from&to.
func (t *ChargingController) GetVehicleSessions(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := strconv.ParseInt(c.Params("tokenId"), 10, 64)
	if err != nil || tokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "valid tokenId path param required")
	}
	if err := t.vehicleInTenant(c, tenant, tokenID); err != nil {
		return err
	}
	from, to, err := parseWindow(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	allowed, _ := GetAllowedGroups(c)
	sessions, err := t.chargingSvc.VehicleSessions(c.Context(), tenant, tokenID, allowed, from, to)
	if err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		return fiber.NewError(fiber.StatusInternalServerError, "charging sessions: "+err.Error())
	}
	return c.JSON(fiber.Map{"sessions": sessions})
}

// ExportCSV — GET /charging/export.csv?from&to (optional ?tokenId=N).
func (t *ChargingController) ExportCSV(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	from, to, err := parseWindow(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	filename := "charging-export.csv"
	var sessions []service.ChargingSessionView
	if q := c.Query("tokenId"); q != "" {
		tokenID, perr := strconv.ParseInt(q, 10, 64)
		if perr != nil || tokenID == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "invalid tokenId")
		}
		if err := t.vehicleInTenant(c, tenant, tokenID); err != nil {
			return err
		}
		allowed, _ := GetAllowedGroups(c)
		sessions, err = t.chargingSvc.VehicleSessions(c.Context(), tenant, tokenID, allowed, from, to)
		if err != nil {
			if serr := ScopeUnavailable(err); serr != nil {
				return serr
			}
			return fiber.NewError(fiber.StatusInternalServerError, "charging sessions: "+err.Error())
		}
		filename = fmt.Sprintf("charging-vehicle-%d.csv", tokenID)
	} else {
		allowed, _ := GetAllowedGroups(c)
		fleet, ferr := t.chargingSvc.FleetSummary(c.Context(), tenant, allowed, from, to)
		if ferr != nil {
			if serr := ScopeUnavailable(ferr); serr != nil {
				return serr
			}
			return fiber.NewError(fiber.StatusInternalServerError, "charging summary: "+ferr.Error())
		}
		sessions = fleet.Sessions
	}
	csvText := service.BuildChargingCSV(sessions)
	c.Set(fiber.HeaderContentType, "text/csv")
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
	return c.SendString(csvText)
}
