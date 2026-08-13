package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// GeofencesController exposes tenant-scoped CRUD over geofences and manual
// vehicle assignments. All handlers run behind the tenant middleware (JWT +
// Tenant-Id).
//
// After a successful mutation the affected attestations are (re)published
// best-effort in a detached goroutine: the tenant geofence catalog (subject =
// client-id DID) on any geofence change, and a vehicle's manual-membership CE
// (subject = vehicle DID) on assignment changes. The DB is the source of truth;
// a publish failure never fails the request (mirrors fleet groups).
type GeofencesController struct {
	logger     *zerolog.Logger
	geofences  *service.GeofenceService
	attest     service.AttestService
	detection  *service.GeofenceDetectionService
	vehicleSvc *service.VehicleService
}

func NewGeofencesController(logger *zerolog.Logger, geofences *service.GeofenceService, attest service.AttestService, detection *service.GeofenceDetectionService, vehicleSvc *service.VehicleService) *GeofencesController {
	return &GeofencesController{logger: logger, geofences: geofences, attest: attest, detection: detection, vehicleSvc: vehicleSvc}
}

type createGeofenceRequest struct {
	Name          string          `json:"name"`
	Color         string          `json:"color"`
	Geometry      json.RawMessage `json:"geometry"`
	SpeedLimitKph *int            `json:"speedLimitKph"`
	Scope         string          `json:"scope"`
	GroupIDs      []string        `json:"groupIds"`
}

type updateGeofenceRequest struct {
	Name          *string         `json:"name"`
	Color         *string         `json:"color"`
	Geometry      json.RawMessage `json:"geometry"`
	SpeedLimitKph *int            `json:"speedLimitKph"`
	Scope         *string         `json:"scope"`
	GroupIDs      []string        `json:"groupIds"`
}

// GeofenceResponse is the JSON shape the frontend consumes (camelCase).
type GeofenceResponse struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Color         string          `json:"color"`
	Geometry      json.RawMessage `json:"geometry"`
	AreaM2        float64         `json:"areaM2"`
	SpeedLimitKph *int            `json:"speedLimitKph"`
	Scope         string          `json:"scope"`
	GroupIDs      []string        `json:"groupIds"`
	VehicleCount  int             `json:"vehicleCount"`
	CreatedBy     string          `json:"createdBy"`
	CreatedAt     string          `json:"createdAt"`
	UpdatedAt     string          `json:"updatedAt"`
}

func toGeofenceResponse(g *dbmodels.Geofence, count int) GeofenceResponse {
	var speed *int
	if g.SpeedLimitKPH.Valid {
		v := g.SpeedLimitKPH.Int
		speed = &v
	}
	groupIDs := []string(g.GroupIds)
	if groupIDs == nil {
		groupIDs = []string{}
	}
	return GeofenceResponse{
		ID:            g.ID,
		Name:          g.Name,
		Color:         g.Color,
		Geometry:      json.RawMessage(g.Geometry),
		AreaM2:        g.AreaM2,
		SpeedLimitKph: speed,
		Scope:         g.Scope,
		GroupIDs:      groupIDs,
		VehicleCount:  count,
		CreatedBy:     g.CreatedBy,
		CreatedAt:     g.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:     g.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

// GetGeofences — GET /fleet/geofences
func (gc *GeofencesController) GetGeofences(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	rows, err := gc.geofences.ListGeofences(c.Context(), tenant)
	if err != nil {
		gc.logger.Err(err).Str("tenant", tenant.ID).Msg("list geofences")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list geofences")
	}
	out := make([]GeofenceResponse, len(rows))
	for i, g := range rows {
		out[i] = toGeofenceResponse(g.Geofence, g.VehicleCount)
	}
	// Limited members see counts over their accessible vehicles only. Geofence
	// panels are small, so per-geofence recount is fine.
	if allowed, limited := GetAllowedGroups(c); limited {
		accessible, aerr := gc.vehicleSvc.AccessibleTokenIDs(c.Context(), tenant, allowed)
		if aerr != nil {
			// The counts are per-member, so an unresolvable scope cannot be
			// papered over with the tenant-wide numbers already in `out`.
			if serr := ScopeUnavailable(aerr); serr != nil {
				gc.logger.Err(aerr).Str("tenant", tenant.ID).Msg("fleet group scope unavailable")
				return serr
			}
			gc.logger.Err(aerr).Str("tenant", tenant.ID).Msg("resolve accessible token ids for geofence counts")
		} else {
			for i, g := range rows {
				ids, ierr := gc.geofences.EffectiveTokenIDs(c.Context(), tenant, g.Geofence)
				if ierr != nil {
					if serr := ScopeUnavailable(ierr); serr != nil {
						return serr
					}
					// Fail closed rather than `continue`: leaving the entry alone
					// would show this limited member the tenant-wide count that
					// out[i] still holds.
					gc.logger.Err(ierr).Str("geofence", g.Geofence.ID).Msg("recount geofence vehicles for member")
					out[i].VehicleCount = 0
					continue
				}
				n := 0
				for _, id := range ids {
					if accessible[id] {
						n++
					}
				}
				out[i].VehicleCount = n
			}
		}
	}
	return c.JSON(fiber.Map{"geofences": out})
}

// GetGeofence — GET /fleet/geofences/:id
func (gc *GeofencesController) GetGeofence(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	g, err := gc.geofences.GetGeofence(c.Context(), tenant.ID, c.Params("id"))
	if err != nil {
		return gc.mapServiceError(err, "get geofence")
	}
	ids, err := gc.geofences.EffectiveTokenIDs(c.Context(), tenant, g)
	if err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		gc.logger.Err(err).Str("geofence", g.ID).Msg("count geofence vehicles")
	}
	restricted, rerr := gc.restrictToAccessible(c, tenant, ids)
	if rerr != nil {
		if serr := ScopeUnavailable(rerr); serr != nil {
			return serr
		}
	} else {
		ids = restricted
	}
	return c.JSON(toGeofenceResponse(g, len(ids)))
}

// GetGeofenceVehicles — GET /fleet/geofences/:id/vehicles
//
// Returns the token ids the geofence currently resolves to across its scope
// (all = tenant fleet, group = members of its groups, manual = explicit
// assignments). The manage-vehicles UI uses it to seed the assigned set.
func (gc *GeofencesController) GetGeofenceVehicles(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	g, err := gc.geofences.GetGeofence(c.Context(), tenant.ID, c.Params("id"))
	if err != nil {
		return gc.mapServiceError(err, "get geofence")
	}
	ids, err := gc.geofences.EffectiveTokenIDs(c.Context(), tenant, g)
	if err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		gc.logger.Err(err).Str("geofence", g.ID).Msg("geofence vehicles")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load geofence vehicles")
	}
	ids, err = gc.restrictToAccessible(c, tenant, ids)
	if err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load geofence vehicles")
	}
	if ids == nil {
		ids = []int64{}
	}
	return c.JSON(fiber.Map{"tokenIds": ids})
}

// CreateGeofence — POST /fleet/geofences
func (gc *GeofencesController) CreateGeofence(c *fiber.Ctx) error {
	if err := RequireFullAccess(c); err != nil {
		return err
	}
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	createdBy, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "could not resolve caller wallet")
	}
	var req createGeofenceRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if req.Name == "" || req.Color == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name and color are required")
	}
	if !hexColorRe.MatchString(req.Color) {
		return fiber.NewError(fiber.StatusBadRequest, "color must be a #RRGGBB hex value")
	}
	if len(req.Geometry) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "geometry is required")
	}
	g, err := gc.geofences.CreateGeofence(c.Context(), tenant, createdBy.Hex(), service.GeofenceInput{
		Name:          req.Name,
		Color:         req.Color,
		Geometry:      req.Geometry,
		SpeedLimitKPH: req.SpeedLimitKph,
		Scope:         req.Scope,
		GroupIDs:      req.GroupIDs,
	})
	if err != nil {
		return gc.mapServiceError(err, "create geofence")
	}
	gc.republishCatalog(tenant)
	return c.Status(fiber.StatusCreated).JSON(toGeofenceResponse(g, 0))
}

// UpdateGeofence — PATCH /fleet/geofences/:id
func (gc *GeofencesController) UpdateGeofence(c *fiber.Ctx) error {
	if err := RequireFullAccess(c); err != nil {
		return err
	}
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	var req updateGeofenceRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if req.Color != nil && *req.Color != "" && !hexColorRe.MatchString(*req.Color) {
		return fiber.NewError(fiber.StatusBadRequest, "color must be a #RRGGBB hex value")
	}
	g, dropped, err := gc.geofences.UpdateGeofence(c.Context(), tenant, c.Params("id"), service.GeofencePatch{
		Name:          req.Name,
		Color:         req.Color,
		Geometry:      req.Geometry,
		SpeedLimitKPH: req.SpeedLimitKph,
		Scope:         req.Scope,
		GroupIDs:      req.GroupIDs,
	})
	if err != nil {
		return gc.mapServiceError(err, "update geofence")
	}
	// Catalog changed; republish it. Vehicles whose manual assignment was
	// dropped (scope moved off manual) get their per-vehicle CE republished too.
	gc.republishCatalog(tenant)
	gc.republishVehicles(tenant, dropped)
	ids, _ := gc.geofences.EffectiveTokenIDs(c.Context(), tenant, g)
	return c.JSON(toGeofenceResponse(g, len(ids)))
}

// DeleteGeofence — DELETE /fleet/geofences/:id
func (gc *GeofencesController) DeleteGeofence(c *fiber.Ctx) error {
	if err := RequireFullAccess(c); err != nil {
		return err
	}
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	members, err := gc.geofences.DeleteGeofence(c.Context(), tenant.ID, c.Params("id"))
	if err != nil {
		return gc.mapServiceError(err, "delete geofence")
	}
	gc.republishCatalog(tenant)
	gc.republishVehicles(tenant, members)
	return c.SendStatus(fiber.StatusNoContent)
}

// AddVehicleToGeofence — POST /fleet/vehicles/:tokenID/geofence/:geofenceID
func (gc *GeofencesController) AddVehicleToGeofence(c *fiber.Ctx) error {
	if err := RequireFullAccess(c); err != nil {
		return err
	}
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	geofenceID := c.Params("geofenceID")
	if _, err := gc.geofences.AddVehicle(c.Context(), tenant.ID, int64(tokenID), geofenceID); err != nil {
		return gc.mapServiceError(err, "assign vehicle to geofence")
	}
	gc.republishVehicle(tenant, int64(tokenID))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"tokenId": tokenID, "geofenceId": geofenceID})
}

// RemoveVehicleFromGeofence — DELETE /fleet/vehicles/:tokenID/geofence/:geofenceID
func (gc *GeofencesController) RemoveVehicleFromGeofence(c *fiber.Ctx) error {
	if err := RequireFullAccess(c); err != nil {
		return err
	}
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	geofenceID := c.Params("geofenceID")
	if _, err := gc.geofences.RemoveVehicle(c.Context(), tenant.ID, int64(tokenID), geofenceID); err != nil {
		return gc.mapServiceError(err, "unassign vehicle from geofence")
	}
	gc.republishVehicle(tenant, int64(tokenID))
	return c.SendStatus(fiber.StatusNoContent)
}

// republishCatalog (re)publishes the tenant's geofence catalog attestation
// (subject = client-id DID) in a detached goroutine. Best-effort.
func (gc *GeofencesController) republishCatalog(tenant models.Tenant) {
	if gc.attest == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		defs, err := gc.geofences.TenantGeofenceDefs(ctx, tenant.ID)
		if err != nil {
			gc.logger.Err(err).Str("tenant", tenant.ID).Msg("load geofence catalog for attestation")
			return
		}
		gc.attestWithRetry(ctx, "catalog tenant="+tenant.ID, func() (string, error) {
			return gc.attest.AttestTenantGeofences(tenant, defs)
		})
	}()
}

// republishVehicles (re)publishes each vehicle's manual geofence-membership
// attestation (subject = vehicle DID) in one detached goroutine. Best-effort.
func (gc *GeofencesController) republishVehicles(tenant models.Tenant, tokenIDs []int64) {
	if gc.attest == nil || len(tokenIDs) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		for _, tid := range tokenIDs {
			ids, err := gc.geofences.VehicleManualGeofenceIDs(ctx, tenant.ID, tid)
			if err != nil {
				gc.logger.Err(err).Str("tenant", tenant.ID).Int64("token_id", tid).
					Msg("load vehicle geofences for attestation")
				continue
			}
			tidCopy := tid
			gc.attestWithRetry(ctx, "vehicle "+tenant.ID, func() (string, error) {
				return gc.attest.AttestVehicleGeofences(tenant, uint64(tidCopy), ids)
			})
		}
	}()
}

// republishVehicle is the single-vehicle convenience over republishVehicles.
func (gc *GeofencesController) republishVehicle(tenant models.Tenant, tokenID int64) {
	gc.republishVehicles(tenant, []int64{tokenID})
}

// attestWithRetry runs a publish func with up to 3 attempts and exponential
// backoff. Best-effort — the final failure is logged at error level.
func (gc *GeofencesController) attestWithRetry(ctx context.Context, label string, fn func() (string, error)) {
	const maxAttempts = 3
	backoff := time.Second
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		eventID, err := fn()
		if err == nil {
			gc.logger.Info().Str("what", label).Str("event_id", eventID).Msg("published geofence attestation")
			return
		}
		gc.logger.Warn().Err(err).Str("what", label).Int("attempt", attempt).Msg("publish geofence attestation failed")
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	gc.logger.Error().Str("what", label).Msg("gave up publishing geofence attestation after retries")
}

// GetTripGeofences — GET /telemetry/:tokenID/trip-geofences?from=...&to=...
// Entry point 1: returns the geofences the vehicle's telemetry crossed within
// [from, to] (a trip window), with per-pass enter/exit/dwell + speed. Passes are
// computed on demand and cached; a repeat call for a covered window is a pure
// cache read. Graceful 200 + permissionsRequired when the dev license lacks
// SACD permissions on the vehicle (mirrors the other telemetry endpoints).
func (gc *GeofencesController) GetTripGeofences(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	fromStr, toStr := c.Query("from"), c.Query("to")
	if fromStr == "" || toStr == "" {
		return fiber.NewError(fiber.StatusBadRequest, "from, to query params are required")
	}
	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "from must be an RFC3339 timestamp")
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "to must be an RFC3339 timestamp")
	}
	if !to.After(from) {
		return fiber.NewError(fiber.StatusBadRequest, "to must be after from")
	}

	if allowed, limited := GetAllowedGroups(c); limited {
		if _, verr := gc.vehicleSvc.GetVehicle(c.Context(), tenant, int64(tokenID), allowed); verr != nil {
			if serr := ScopeUnavailable(verr); serr != nil {
				return serr
			}
			return fiber.NewError(fiber.StatusNotFound, "vehicle not found")
		}
	}

	crossings, err := gc.detection.TripGeofences(c.Context(), tenant, int64(tokenID), from, to)
	if err != nil {
		if isPermissionError(err) {
			return c.JSON(fiber.Map{
				"geofences":           []service.GeofenceCrossing{},
				"permissionsRequired": true,
				"devLicense":          tenant.ClientID,
			})
		}
		return gc.mapServiceError(err, "trip geofences")
	}
	return c.JSON(fiber.Map{"geofences": crossings})
}

// GetGeofenceScanTargets — GET /fleet/geofences/:id/scan-targets
// Entry point 2, step 1: the effective vehicles to scan for this geofence,
// capped. The client pages these token ids through GetGeofencePasses in batches
// (progressive results). `capped` is true when the effective set was truncated.
func (gc *GeofencesController) GetGeofenceScanTargets(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	ids, total, capped, err := gc.detection.ScanTargets(c.Context(), tenant, c.Params("id"))
	if err != nil {
		return gc.mapServiceError(err, "geofence scan targets")
	}
	if _, limited := GetAllowedGroups(c); limited {
		ids, err = gc.restrictToAccessible(c, tenant, ids)
		if err != nil {
			if serr := ScopeUnavailable(err); serr != nil {
				return serr
			}
			return fiber.NewError(fiber.StatusInternalServerError, "failed to load scan targets")
		}
		// The member's effective scan set is what they can see, not the full fleet.
		total = len(ids)
	}
	return c.JSON(fiber.Map{"tokenIds": ids, "total": total, "capped": capped})
}

// GetGeofencePasses — GET /fleet/geofences/:id/passes?from=...&to=...&tokenIds=a,b,c
// Entry point 2, step 2: the passes through this geofence in [from, to] for a
// batch of vehicles. Window is capped server-side at 3 days. `tokenIds` is
// required — the client pages the scan-targets through here so results stream in.
func (gc *GeofencesController) GetGeofencePasses(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	fromStr, toStr := c.Query("from"), c.Query("to")
	if fromStr == "" || toStr == "" {
		return fiber.NewError(fiber.StatusBadRequest, "from, to query params are required")
	}
	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "from must be an RFC3339 timestamp")
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "to must be an RFC3339 timestamp")
	}
	if !to.After(from) {
		return fiber.NewError(fiber.StatusBadRequest, "to must be after from")
	}
	raw := c.Query("tokenIds")
	if raw == "" {
		return fiber.NewError(fiber.StatusBadRequest, "tokenIds query param is required")
	}
	var ids []int64
	for _, part := range strings.Split(raw, ",") {
		if id, perr := strconv.ParseInt(strings.TrimSpace(part), 10, 64); perr == nil {
			ids = append(ids, id)
		}
	}

	ids, err = gc.restrictToAccessible(c, tenant, ids)
	if err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load geofence passes")
	}

	results, err := gc.detection.WindowScan(c.Context(), tenant, c.Params("id"), ids, from, to)
	if err != nil {
		if errors.Is(err, service.ErrScanWindowTooLarge) {
			return fiber.NewError(fiber.StatusBadRequest, "scan window exceeds the maximum of 3 days")
		}
		return gc.mapServiceError(err, "geofence passes")
	}
	return c.JSON(fiber.Map{"results": results})
}

// restrictToAccessible intersects token ids with the vehicles a limited member
// may see (union of their allowed groups). Unrestricted callers pass through.
func (gc *GeofencesController) restrictToAccessible(c *fiber.Ctx, tenant models.Tenant, ids []int64) ([]int64, error) {
	allowed, limited := GetAllowedGroups(c)
	if !limited || len(ids) == 0 {
		return ids, nil
	}
	accessible, err := gc.vehicleSvc.AccessibleTokenIDs(c.Context(), tenant, allowed)
	if err != nil {
		// Never return ids unfiltered on failure — that is the whole geofence's
		// vehicle set handed to a member scoped to part of it.
		gc.logger.Err(err).Str("tenant", tenant.ID).Msg("resolve accessible token ids")
		return nil, err
	}
	kept := ids[:0:0]
	for _, id := range ids {
		if accessible[id] {
			kept = append(kept, id)
		}
	}
	return kept, nil
}

// mapServiceError translates geofence service sentinel errors into HTTP errors.
func (gc *GeofencesController) mapServiceError(err error, msg string) error {
	switch {
	case errors.Is(err, service.ErrGroupScopeUnavailable):
		// A group-scoped geofence could not be validated or resolved against
		// fleet-tenancy-api. Our failure, so 503 — and never a silent "no groups".
		gc.logger.Err(err).Msg(msg)
		return fiber.NewError(fiber.StatusServiceUnavailable, "authorization service unavailable")
	case errors.Is(err, service.ErrGeofenceNotFound):
		return fiber.NewError(fiber.StatusNotFound, "geofence not found")
	case errors.Is(err, service.ErrVehicleNotFound):
		return fiber.NewError(fiber.StatusNotFound, "vehicle not found")
	case errors.Is(err, service.ErrGeofenceNameExists):
		return fiber.NewError(fiber.StatusConflict, "a geofence with this name already exists")
	case errors.Is(err, service.ErrInvalidScope):
		return fiber.NewError(fiber.StatusBadRequest, "scope must be one of all, group, or manual (and assignment requires a manual-scope geofence)")
	case errors.Is(err, service.ErrInvalidGeometry):
		return fiber.NewError(fiber.StatusBadRequest, "geometry must be a valid GeoJSON Polygon")
	case errors.Is(err, service.ErrUnknownGroup):
		return fiber.NewError(fiber.StatusBadRequest, "one or more group ids do not exist for this tenant")
	case errors.Is(err, service.ErrGroupScopeNeedsGroups):
		return fiber.NewError(fiber.StatusBadRequest, "a group-scoped geofence requires at least one group id")
	default:
		gc.logger.Err(err).Msg(msg)
		return fiber.NewError(fiber.StatusInternalServerError, msg)
	}
}
