package controllers

import (
	"context"
	"fmt"
	"strings"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// Curated set of signals the vehicle-details view needs for "latest" reads.
// Kept server-side so the frontend doesn't have to know about telemetry-api's
// signal naming. Add or remove based on what new cards need.
var curatedLatestSignals = []string{
	"powertrainTransmissionTravelledDistance",           // odometer (km)
	"powertrainFuelSystemRelativeLevel",                 // fuel % (ICE)
	"powertrainCombustionEngineECT",                     // coolant temp (°C)
	"lowVoltageBatteryCurrentVoltage",                   // 12V battery (V)
	"powertrainCombustionEngineDieselExhaustFluidLevel", // AdBlue % (diesel)
	"powertrainTractionBatteryStateOfChargeCurrent",     // EV SoC %
	"speed",                                             // current speed (km/h)
}

type TelemetryController struct {
	logger     *zerolog.Logger
	settings   *config.Settings
	vehicleSvc *service.VehicleService
	telemetry  service.TelemetryAPIService
}

func NewTelemetryController(
	logger *zerolog.Logger,
	settings *config.Settings,
	vehicleSvc *service.VehicleService,
	telemetry service.TelemetryAPIService,
) *TelemetryController {
	return &TelemetryController{
		logger:     logger,
		settings:   settings,
		vehicleSvc: vehicleSvc,
		telemetry:  telemetry,
	}
}

// vehicleInTenant reports whether the tokenID is one of the tenant's synced vehicles.
func (t *TelemetryController) vehicleInTenant(ctx context.Context, tenantID string, tokenID uint64) bool {
	_, err := t.vehicleSvc.GetVehicle(ctx, tenantID, int64(tokenID))
	return err == nil
}

// isPermissionError matches 403s coming back from token-exchange-api when the
// dev license lacks SACDs on a vehicle. Same heuristic as the glovebox list
// endpoint — keep them in sync.
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "lacks permissions") || strings.Contains(s, "status code 403")
}

// GetLatest — GET /telemetry/:tokenID/latest. Returns the curated signal set
// for the selected vehicle. Graceful 200 + permissionsRequired:true when the
// tenant's dev license lacks SACDs on the vehicle, so the frontend can banner.
func (t *TelemetryController) GetLatest(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	if !t.vehicleInTenant(c.Context(), tenant.ID, tokenID) {
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}

	latest, err := t.telemetry.Latest(tenant, tokenID, curatedLatestSignals)
	if err != nil {
		if isPermissionError(err) {
			t.logger.Warn().Uint64("tokenID", tokenID).Msg("telemetry: dev license lacks SACD permissions")
			return c.JSON(fiber.Map{
				"signals":             map[string]interface{}{},
				"permissionsRequired": true,
				"devLicense":          tenant.ClientID,
			})
		}
		t.logger.Err(err).Uint64("tokenID", tokenID).Msg("telemetry latest failed")
		return fiber.NewError(fiber.StatusBadGateway, "telemetry latest failed: "+err.Error())
	}
	return c.JSON(fiber.Map{"signals": latest})
}

// GetFleetLocations — GET /telemetry/locations. Returns last-known coordinates
// for all integrated vehicles in the tenant in a single telemetry-api request.
func (t *TelemetryController) GetFleetLocations(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	vehicles, err := t.vehicleSvc.ListVehicles(c.Context(), tenant.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "list vehicles: "+err.Error())
	}
	tokenIDs := make([]uint64, 0, len(vehicles))
	for _, v := range vehicles {
		if v.TokenID > 0 {
			tokenIDs = append(tokenIDs, uint64(v.TokenID))
		}
	}
	result, err := t.telemetry.FleetLocations(tenant, tokenIDs)
	if err != nil {
		t.logger.Err(err).Msg("fleet locations failed")
		return fiber.NewError(fiber.StatusBadGateway, "fleet locations failed: "+err.Error())
	}
	// Rekey as strings for JSON (JS can't safely represent uint64 as number).
	out := make(map[string]interface{}, len(result.Locations))
	for id, coords := range result.Locations {
		out[fmt.Sprintf("%d", id)] = coords
	}
	noPerms := make([]string, len(result.NoPermissions))
	for i, id := range result.NoPermissions {
		noPerms[i] = fmt.Sprintf("%d", id)
	}
	return c.JSON(fiber.Map{"locations": out, "noPermissions": noPerms})
}

// GetTimeSeries — GET /telemetry/:tokenID/timeseries?signal=X&from=...&to=...&interval=...
// Returns aggregation buckets for the requested signal. Used by the Speed and
// Distance cards' 7-day bar charts.
func (t *TelemetryController) GetTimeSeries(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	signal := c.Query("signal")
	from := c.Query("from")
	to := c.Query("to")
	interval := c.Query("interval", "1d")
	if signal == "" || from == "" || to == "" {
		return fiber.NewError(fiber.StatusBadRequest, "signal, from, to query params are required")
	}
	if !t.vehicleInTenant(c.Context(), tenant.ID, tokenID) {
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}

	buckets, err := t.telemetry.TimeSeries(tenant, tokenID, signal, from, to, interval)
	if err != nil {
		if isPermissionError(err) {
			return c.JSON(fiber.Map{
				"buckets":             []interface{}{},
				"permissionsRequired": true,
				"devLicense":          tenant.ClientID,
			})
		}
		t.logger.Err(err).Str("signal", signal).Uint64("tokenID", tokenID).Msg("telemetry timeseries failed")
		return fiber.NewError(fiber.StatusBadGateway, "telemetry time-series failed: "+err.Error())
	}
	return c.JSON(fiber.Map{"signal": signal, "interval": interval, "buckets": buckets})
}
