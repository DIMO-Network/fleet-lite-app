// api/internal/controllers/tco.go
package controllers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

type TCOController struct {
	logger     *zerolog.Logger
	tcoSvc     *service.TCOService
	vehicleSvc *service.VehicleService
}

func NewTCOController(logger *zerolog.Logger, tcoSvc *service.TCOService, vehicleSvc *service.VehicleService) *TCOController {
	return &TCOController{logger: logger, tcoSvc: tcoSvc, vehicleSvc: vehicleSvc}
}

// vehicleInTenant reports whether the tokenID is one of the tenant's synced vehicles.
func (t *TCOController) vehicleInTenant(ctx context.Context, tenantID string, tokenID int64) bool {
	_, err := t.vehicleSvc.GetVehicle(ctx, tenantID, tokenID)
	return err == nil
}

// GetSettings — GET /tco/settings?tokenId=N.
func (t *TCOController) GetSettings(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := strconv.ParseInt(c.Query("tokenId"), 10, 64)
	if err != nil || tokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "valid tokenId query param required")
	}
	if !t.vehicleInTenant(c.Context(), tenant.ID, tokenID) {
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}
	settings, err := t.tcoSvc.GetSettings(c.Context(), tenant.ID, tokenID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "get tco settings: "+err.Error())
	}
	return c.JSON(settings)
}

// PutSettingsRequest is the body for PUT /tco/settings.
type PutSettingsRequest struct {
	TokenID         int64    `json:"tokenId"`
	PurchasePrice   *float64 `json:"purchasePrice"`
	PurchaseDate    *string  `json:"purchaseDate"`
	UsefulLifeYears *int     `json:"usefulLifeYears"`
	Currency        string   `json:"currency"`
}

// PutSettings — PUT /tco/settings.
func (t *TCOController) PutSettings(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	var req PutSettingsRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body: "+err.Error())
	}
	if req.TokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "tokenId is required")
	}
	if !t.vehicleInTenant(c.Context(), tenant.ID, req.TokenID) {
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}
	currency := req.Currency
	if currency == "" {
		currency = "USD"
	}
	settings := service.TCOSettings{
		VehicleTokenID:  req.TokenID,
		PurchasePrice:   req.PurchasePrice,
		PurchaseDate:    req.PurchaseDate,
		UsefulLifeYears: req.UsefulLifeYears,
		Currency:        currency,
	}
	if err := t.tcoSvc.UpsertSettings(c.Context(), tenant.ID, req.TokenID, settings); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "save tco settings: "+err.Error())
	}
	return c.JSON(settings)
}

// GetSummary — GET /tco/summary. Fleet-wide rollup.
func (t *TCOController) GetSummary(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	summary, err := t.tcoSvc.FleetSummary(c.Context(), tenant)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "tco summary: "+err.Error())
	}
	return c.JSON(summary)
}

// GetVehicleDetail — GET /tco/vehicle/:tokenId.
func (t *TCOController) GetVehicleDetail(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := strconv.ParseInt(c.Params("tokenId"), 10, 64)
	if err != nil || tokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "valid tokenId path param required")
	}
	if !t.vehicleInTenant(c.Context(), tenant.ID, tokenID) {
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}
	summary, err := t.tcoSvc.VehicleSummary(c.Context(), tenant, tokenID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "tco vehicle summary: "+err.Error())
	}
	return c.JSON(summary)
}

// ExportCSV — GET /tco/export.csv (optionally ?tokenId=N for a single vehicle).
func (t *TCOController) ExportCSV(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	var summaries []service.VehicleTCOSummary
	filename := "tco-export.csv"
	if q := c.Query("tokenId"); q != "" {
		tokenID, err := strconv.ParseInt(q, 10, 64)
		if err != nil || tokenID == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "invalid tokenId")
		}
		if !t.vehicleInTenant(c.Context(), tenant.ID, tokenID) {
			return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
		}
		summary, err := t.tcoSvc.VehicleSummary(c.Context(), tenant, tokenID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "tco vehicle summary: "+err.Error())
		}
		summaries = []service.VehicleTCOSummary{*summary}
		filename = fmt.Sprintf("tco-vehicle-%d.csv", tokenID)
	} else {
		fleet, err := t.tcoSvc.FleetSummary(c.Context(), tenant)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "tco summary: "+err.Error())
		}
		summaries = fleet.Vehicles
	}
	csvText := service.BuildCSV(summaries)
	c.Set(fiber.HeaderContentType, "text/csv")
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
	return c.SendString(csvText)
}
