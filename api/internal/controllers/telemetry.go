package controllers

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
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
	"powertrainTractionBatteryStateOfChargeCurrent",     // EV SoC %
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

// isPermissionError matches authorization failures from either token-exchange-api
// (403 / "lacks permissions" when the dev license lacks SACDs) or from the
// telemetry-api GraphQL layer ("unauthorized: missing required privilege(s) ...")
// when the vehicle JWT doesn't carry a required privilege (e.g. VEHICLE_ALL_TIME_LOCATION
// for segments). Keep in sync with the glovebox list endpoint.
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "lacks permissions") ||
		strings.Contains(s, "status code 403") ||
		strings.Contains(s, "unauthorized")
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
// for the tenant's vehicles (bounded-concurrency fan-out to telemetry-api; one
// request per vehicle is forced by per-vehicle JWT auth). An optional
// ?tokenIds=1,2,3 restricts the call to a subset — the map view pages the
// fleet through this so markers stream in instead of waiting on one big call.
// Requested ids are intersected with the tenant's vehicles, so a subset call
// can never probe another tenant's tokens.
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
	if raw := c.Query("tokenIds"); raw != "" {
		allowed := make(map[uint64]bool, len(tokenIDs))
		for _, id := range tokenIDs {
			allowed[id] = true
		}
		subset := make([]uint64, 0, 32)
		for _, part := range strings.Split(raw, ",") {
			id, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64)
			if err == nil && allowed[id] {
				subset = append(subset, id)
			}
		}
		tokenIDs = subset
	}
	// force=true (the map's manual refresh) bypasses the per-tenant cache.
	force := c.Query("force") == "true"
	result, err := t.telemetry.FleetLocations(c.Context(), tenant, tokenIDs, force)
	if err != nil {
		t.logger.Err(err).Msg("fleet locations failed")
		return fiber.NewError(fiber.StatusBadGateway, "fleet locations failed: "+err.Error())
	}
	// Write fresh fixes + the pull timestamp through to the vehicles table:
	//  - last_lat/last_lon/last_seen for vehicles that returned coords, so the
	//    next first paint renders markers + "last seen" instantly from Postgres;
	//  - location_pulled_at for every vehicle actually queried this call
	//    (result.Fetched — coords, no-data, or no-permission alike, but NOT
	//    cache hits), so the freshness window can suppress re-pulls.
	// Best-effort and detached: never blocks or fails the response; the result
	// above is already authoritative for this call.
	if len(result.Locations) > 0 || len(result.Fetched) > 0 {
		locs := result.Locations
		fetched := result.Fetched
		tenantID := tenant.ID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			t.vehicleSvc.UpsertLastLocations(ctx, tenantID, locs)
			t.vehicleSvc.StampLocationPulled(ctx, tenantID, fetched)
		}()
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

// GetSegments — GET /telemetry/:tokenID/segments?from=...&to=...&mechanism=...
// Returns detected trips for the vehicle in the window, newest first.
//
// When `mechanism` is omitted the detection method follows the b2b fleet
// manager's heuristic: aftermarket devices only support frequencyAnalysis;
// everything else tries ignitionDetection first and falls back to
// frequencyAnalysis when it detects nothing. An explicit `mechanism` (one of
// service.ValidSegmentMechanisms) overrides the heuristic and disables the
// fallback, so the caller sees exactly the detector they asked for.
func (t *TelemetryController) GetSegments(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	from := c.Query("from")
	to := c.Query("to")
	if from == "" || to == "" {
		return fiber.NewError(fiber.StatusBadRequest, "from, to query params are required")
	}
	vehicle, verr := t.vehicleSvc.GetVehicle(c.Context(), tenant.ID, int64(tokenID))
	if verr != nil {
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}

	// Explicit mechanism (from the details-screen picker) overrides the
	// heuristic and disables the auto-fallback below.
	explicit := c.Query("mechanism")
	if explicit != "" && !service.IsValidSegmentMechanism(explicit) {
		return fiber.NewError(fiber.StatusBadRequest, "mechanism must be one of: "+strings.Join(service.ValidSegmentMechanisms, ", "))
	}

	mechanism := explicit
	if mechanism == "" {
		mechanism = "ignitionDetection"
		if vehicle.AftermarketDevice != nil && vehicle.AftermarketDevice.TokenID > 0 {
			mechanism = "frequencyAnalysis"
		}
	}
	segments, err := t.telemetry.Segments(tenant, tokenID, from, to, mechanism)
	if explicit == "" && err == nil && len(segments) == 0 && mechanism == "ignitionDetection" {
		// Some synthetic integrations never report ignition — retry with the
		// frequency-based detector before concluding there were no trips.
		mechanism = "frequencyAnalysis"
		segments, err = t.telemetry.Segments(tenant, tokenID, from, to, mechanism)
	}
	if err != nil {
		if isPermissionError(err) {
			return c.JSON(fiber.Map{
				"segments":            []interface{}{},
				"permissionsRequired": true,
				"devLicense":          tenant.ClientID,
			})
		}
		t.logger.Err(err).Uint64("tokenID", tokenID).Msg("telemetry segments failed")
		return fiber.NewError(fiber.StatusBadGateway, "telemetry segments failed: "+err.Error())
	}

	// Newest trips first — the panel shows the most recent activity on top.
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].Start.Timestamp > segments[j].Start.Timestamp
	})
	return c.JSON(fiber.Map{"segments": segments, "mechanism": mechanism})
}

// GetTripRoute — GET /telemetry/:tokenID/route?from=...&to=... Returns the
// sampled location points across one trip's window, for drawing its polyline.
func (t *TelemetryController) GetTripRoute(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	from := c.Query("from")
	to := c.Query("to")
	if from == "" || to == "" {
		return fiber.NewError(fiber.StatusBadRequest, "from, to query params are required")
	}
	if !t.vehicleInTenant(c.Context(), tenant.ID, tokenID) {
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}

	points, err := t.telemetry.RoutePoints(tenant, tokenID, from, to)
	if err != nil {
		if isPermissionError(err) {
			return c.JSON(fiber.Map{
				"points":              []interface{}{},
				"permissionsRequired": true,
				"devLicense":          tenant.ClientID,
			})
		}
		t.logger.Err(err).Uint64("tokenID", tokenID).Msg("telemetry route failed")
		return fiber.NewError(fiber.StatusBadGateway, "telemetry route failed: "+err.Error())
	}
	return c.JSON(fiber.Map{"points": points})
}

// GetTripReplay — GET /telemetry/:tokenID/replay?from=...&to=...
// Returns timestamped GPS waypoints and behavior events for a trip's window,
// used to animate route playback in the replay modal. `from`/`to` are
// required — replay always has an explicit window from the selected trip.
func (t *TelemetryController) GetTripReplay(c *fiber.Ctx) error {
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

	from := c.Query("from")
	to := c.Query("to")
	if from == "" || to == "" {
		return fiber.NewError(fiber.StatusBadRequest, "from, to query params are required")
	}
	if _, err := time.Parse(time.RFC3339, from); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "from must be an RFC3339 timestamp")
	}
	if _, err := time.Parse(time.RFC3339, to); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "to must be an RFC3339 timestamp")
	}

	waypoints, events, err := t.telemetry.TripReplay(tenant, tokenID, from, to)
	if err != nil {
		if isPermissionError(err) {
			return c.JSON(fiber.Map{
				"waypoints":           []service.TripWaypoint{},
				"events":              []service.TripEvent{},
				"permissionsRequired": true,
				"devLicense":          tenant.ClientID,
			})
		}
		t.logger.Err(err).Uint64("tokenID", tokenID).Msg("telemetry trip replay failed")
		return fiber.NewError(fiber.StatusBadGateway, "telemetry trip replay failed: "+err.Error())
	}
	return c.JSON(fiber.Map{"waypoints": waypoints, "events": events})
}
