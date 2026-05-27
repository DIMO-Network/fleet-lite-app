package controllers

import (
	"strings"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// Curated set of signals the vehicle-details view needs for "latest" reads.
// Kept server-side so the frontend doesn't have to know about telemetry-api's
// signal naming. Add or remove based on what new cards need.
var curatedLatestSignals = []string{
	"powertrainTransmissionTravelledDistance",          // odometer (km)
	"powertrainFuelSystemRelativeLevel",                // fuel % (ICE)
	"powertrainCombustionEngineECT",                    // coolant temp (°C)
	"lowVoltageBatteryCurrentVoltage",                  // 12V battery (V)
	"powertrainCombustionEngineDieselExhaustFluidLevel", // AdBlue % (diesel)
	"powertrainTractionBatteryStateOfChargeCurrent",    // EV SoC % — useful if applicable
	"speed",                                            // current speed (km/h)
}

type TelemetryController struct {
	logger      *zerolog.Logger
	settings    *config.Settings
	identity    gateway.IdentityAPI
	telemetry   service.TelemetryAPIService
}

func NewTelemetryController(
	logger *zerolog.Logger,
	settings *config.Settings,
	identity gateway.IdentityAPI,
	telemetry service.TelemetryAPIService,
) *TelemetryController {
	return &TelemetryController{
		logger:    logger,
		settings:  settings,
		identity:  identity,
		telemetry: telemetry,
	}
}

func (t *TelemetryController) ownsVehicle(wallet string, tokenID uint64) (bool, error) {
	vehicles, err := t.identity.FetchVehiclesByWalletAddress(wallet)
	if err != nil {
		return false, err
	}
	for _, v := range vehicles {
		if uint64(v.TokenID) == tokenID {
			return true, nil
		}
	}
	return false, nil
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
// dev license lacks SACDs on the vehicle, so the frontend can show a banner.
func (t *TelemetryController) GetLatest(c *fiber.Ctx) error {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	owns, err := t.ownsVehicle(wallet.Hex(), tokenID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "ownership check failed")
	}
	if !owns {
		return fiber.NewError(fiber.StatusForbidden, "caller does not own this vehicle")
	}

	latest, err := t.telemetry.Latest(tokenID, curatedLatestSignals)
	if err != nil {
		if isPermissionError(err) {
			t.logger.Warn().Uint64("tokenID", tokenID).Msg("telemetry: dev license lacks SACD permissions")
			return c.JSON(fiber.Map{
				"signals":             map[string]interface{}{},
				"permissionsRequired": true,
				"devLicense":          t.settings.DimoAuthClientID.Hex(),
			})
		}
		t.logger.Err(err).Uint64("tokenID", tokenID).Msg("telemetry latest failed")
		return fiber.NewError(fiber.StatusBadGateway, "telemetry latest failed: "+err.Error())
	}
	return c.JSON(fiber.Map{"signals": latest})
}

// GetTimeSeries — GET /telemetry/:tokenID/timeseries?signal=X&from=...&to=...&interval=...
// Returns aggregation buckets for the requested signal. Used by the Speed and
// Distance cards' 7-day bar charts.
func (t *TelemetryController) GetTimeSeries(c *fiber.Ctx) error {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
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

	owns, err := t.ownsVehicle(wallet.Hex(), tokenID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "ownership check failed")
	}
	if !owns {
		return fiber.NewError(fiber.StatusForbidden, "caller does not own this vehicle")
	}

	buckets, err := t.telemetry.TimeSeries(tokenID, signal, from, to, interval)
	if err != nil {
		if isPermissionError(err) {
			return c.JSON(fiber.Map{
				"buckets":             []interface{}{},
				"permissionsRequired": true,
				"devLicense":          t.settings.DimoAuthClientID.Hex(),
			})
		}
		t.logger.Err(err).Str("signal", signal).Uint64("tokenID", tokenID).Msg("telemetry timeseries failed")
		return fiber.NewError(fiber.StatusBadGateway, "telemetry time-series failed: "+err.Error())
	}
	return c.JSON(fiber.Map{"signal": signal, "interval": interval, "buckets": buckets})
}

