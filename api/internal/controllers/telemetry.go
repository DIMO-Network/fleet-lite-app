package controllers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/patrickmn/go-cache"
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
	"powertrainTractionBatteryStateOfChargeCurrent",     // EV SoC % — useful if applicable
	"speed", // current speed (km/h)
}

type TelemetryController struct {
	logger         *zerolog.Logger
	settings       *config.Settings
	vehicleSvc     *service.VehicleService
	telemetry      service.TelemetryAPIService
	locationsCache *cache.Cache // tenantID -> []vehicleLocationJSON (map markers)
}

func NewTelemetryController(
	logger *zerolog.Logger,
	settings *config.Settings,
	vehicleSvc *service.VehicleService,
	telemetry service.TelemetryAPIService,
) *TelemetryController {
	return &TelemetryController{
		logger:         logger,
		settings:       settings,
		vehicleSvc:     vehicleSvc,
		telemetry:      telemetry,
		locationsCache: cache.New(2*time.Minute, 5*time.Minute),
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

// vehicleLocationJSON is one marker on the fleet-overview map.
type vehicleLocationJSON struct {
	TokenID   int64   `json:"tokenId"`
	Title     string  `json:"title"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timestamp string  `json:"timestamp"`
}

// GetLocations — GET /telemetry/locations. Returns the latest GPS fix for each
// of the tenant's vehicles that reports one, for the fleet-overview map.
// Vehicles with no location, or where the dev license lacks SACD permissions,
// are silently omitted. Fetches concurrently (bounded) to keep latency low.
func (t *TelemetryController) GetLocations(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// Per-tenant cache: fleets can have hundreds of vehicles, and each location
	// is a JWT mint + telemetry-api round trip. Caching keeps the map fast and
	// avoids hammering DIMO on every load.
	if cached, found := t.locationsCache.Get(tenant.ID); found {
		return c.JSON(fiber.Map{"locations": cached})
	}

	vehicles, err := t.vehicleSvc.ListVehicles(c.Context(), tenant.ID)
	if err != nil {
		t.logger.Err(err).Str("tenant", tenant.ID).Msg("list vehicles for locations")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list vehicles")
	}

	const maxConcurrent = 20
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var mu sync.Mutex
	out := make([]vehicleLocationJSON, 0, len(vehicles))

	for _, v := range vehicles {
		v := v
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			loc, lerr := t.telemetry.LatestLocation(tenant, uint64(v.TokenID))
			if lerr != nil {
				// A dev license without SACDs on a vehicle is expected — skip quietly.
				if !isPermissionError(lerr) {
					t.logger.Warn().Err(lerr).Int64("tokenID", v.TokenID).Msg("location fetch failed")
				}
				return
			}
			if loc == nil {
				return // vehicle has never reported a location
			}

			mu.Lock()
			out = append(out, vehicleLocationJSON{
				TokenID:   v.TokenID,
				Title:     vehicleTitle(v),
				Latitude:  loc.Latitude,
				Longitude: loc.Longitude,
				Timestamp: loc.Timestamp,
			})
			mu.Unlock()
		}()
	}
	wg.Wait()

	t.locationsCache.Set(tenant.ID, out, cache.DefaultExpiration)
	return c.JSON(fiber.Map{"locations": out})
}

// vehicleTitle builds a "YEAR MAKE MODEL" label, falling back to the token id.
// Mirrors the frontend's formatTitle so map popups match the vehicle cards.
func vehicleTitle(v models.Vehicle) string {
	d := v.Definition
	parts := make([]string, 0, 3)
	if d.Year > 0 {
		parts = append(parts, strconv.Itoa(d.Year))
	}
	if d.Make != "" {
		parts = append(parts, d.Make)
	}
	if d.Model != "" {
		parts = append(parts, d.Model)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Vehicle #%d", v.TokenID)
	}
	return strings.Join(parts, " ")
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
