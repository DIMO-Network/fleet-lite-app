package controllers

import (
	"context"
	"errors"
	"regexp"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

var hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// FleetGroupsController exposes tenant-scoped CRUD over fleet groups and vehicle
// membership. All handlers run behind the tenant middleware (JWT + Tenant-Id).
//
// After a successful mutation, the affected vehicles' group-membership
// attestation is (re)published best-effort in a detached goroutine (Decision 1):
// the DB is the source of truth, the attestation is an eventually-consistent
// mirror, and a publish failure never fails the request.
type FleetGroupsController struct {
	logger *zerolog.Logger
	groups *service.FleetGroupService
	attest service.AttestService
}

func NewFleetGroupsController(logger *zerolog.Logger, groups *service.FleetGroupService, attest service.AttestService) *FleetGroupsController {
	return &FleetGroupsController{logger: logger, groups: groups, attest: attest}
}

type createFleetGroupRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type updateFleetGroupRequest struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

// FleetGroupResponse is the JSON shape the frontend consumes (camelCase).
type FleetGroupResponse struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Color        string    `json:"color"`
	VehicleCount int       `json:"vehicleCount"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func toFleetGroupResponse(g *dbmodels.FleetGroup, count int) FleetGroupResponse {
	return FleetGroupResponse{
		ID:           g.ID,
		Name:         g.Name,
		Color:        g.Color,
		VehicleCount: count,
		CreatedAt:    g.CreatedAt,
		UpdatedAt:    g.UpdatedAt,
	}
}

// GetGroups — GET /fleet/groups
func (fc *FleetGroupsController) GetGroups(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	groups, err := fc.groups.ListGroups(c.Context(), tenant.ID)
	if err != nil {
		fc.logger.Err(err).Str("tenant", tenant.ID).Msg("list fleet groups")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list fleet groups")
	}
	out := make([]FleetGroupResponse, len(groups))
	for i, g := range groups {
		out[i] = toFleetGroupResponse(g.Group, g.VehicleCount)
	}
	return c.JSON(fiber.Map{"groups": out})
}

// GetGroup — GET /fleet/groups/:id
func (fc *FleetGroupsController) GetGroup(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	g, err := fc.groups.GetGroup(c.Context(), tenant.ID, c.Params("id"))
	if err != nil {
		return fc.mapServiceError(err, "get fleet group")
	}
	members, err := fc.groups.GroupMemberTokenIDs(c.Context(), tenant.ID, g.ID)
	if err != nil {
		fc.logger.Err(err).Str("group", g.ID).Msg("count members")
	}
	return c.JSON(toFleetGroupResponse(g, len(members)))
}

// CreateGroup — POST /fleet/groups {name,color}
func (fc *FleetGroupsController) CreateGroup(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	var req createFleetGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if req.Name == "" || req.Color == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name and color are required")
	}
	if !hexColorRe.MatchString(req.Color) {
		return fiber.NewError(fiber.StatusBadRequest, "color must be a #RRGGBB hex value")
	}
	g, err := fc.groups.CreateGroup(c.Context(), tenant.ID, req.Name, req.Color)
	if err != nil {
		return fc.mapServiceError(err, "create fleet group")
	}
	return c.Status(fiber.StatusCreated).JSON(toFleetGroupResponse(g, 0))
}

// UpdateGroup — PATCH /fleet/groups/:id {name?,color?}
func (fc *FleetGroupsController) UpdateGroup(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	var req updateFleetGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if req.Color != nil && *req.Color != "" && !hexColorRe.MatchString(*req.Color) {
		return fiber.NewError(fiber.StatusBadRequest, "color must be a #RRGGBB hex value")
	}
	g, err := fc.groups.UpdateGroup(c.Context(), tenant.ID, c.Params("id"), req.Name, req.Color)
	if err != nil {
		return fc.mapServiceError(err, "update fleet group")
	}
	members, _ := fc.groups.GroupMemberTokenIDs(c.Context(), tenant.ID, g.ID)
	// Recolor/rename changes the document of every member — fan out a re-publish.
	fc.republishVehicles(tenant, members)
	return c.JSON(toFleetGroupResponse(g, len(members)))
}

// DeleteGroup — DELETE /fleet/groups/:id
func (fc *FleetGroupsController) DeleteGroup(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	groupID := c.Params("id")
	// Capture members BEFORE the delete — the cascade removes the join rows.
	members, err := fc.groups.GroupMemberTokenIDs(c.Context(), tenant.ID, groupID)
	if err != nil {
		fc.logger.Err(err).Str("group", groupID).Msg("load members before delete")
	}
	if err := fc.groups.DeleteGroup(c.Context(), tenant.ID, groupID); err != nil {
		return fc.mapServiceError(err, "delete fleet group")
	}
	// Re-publish each former member without this group (LoadVehicleGroups now
	// returns its remaining groups).
	fc.republishVehicles(tenant, members)
	return c.SendStatus(fiber.StatusNoContent)
}

// AddVehicleToGroup — POST /fleet/vehicles/:tokenID/group/:groupID
func (fc *FleetGroupsController) AddVehicleToGroup(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	groupID := c.Params("groupID")
	if _, err := fc.groups.AddVehicle(c.Context(), tenant.ID, int64(tokenID), groupID); err != nil {
		return fc.mapServiceError(err, "add vehicle to group")
	}
	fc.republishVehicle(tenant, int64(tokenID))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"tokenId": tokenID, "groupId": groupID})
}

// RemoveVehicleFromGroup — DELETE /fleet/vehicles/:tokenID/group/:groupID
func (fc *FleetGroupsController) RemoveVehicleFromGroup(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	groupID := c.Params("groupID")
	if _, err := fc.groups.RemoveVehicle(c.Context(), tenant.ID, int64(tokenID), groupID); err != nil {
		return fc.mapServiceError(err, "remove vehicle from group")
	}
	fc.republishVehicle(tenant, int64(tokenID))
	return c.SendStatus(fiber.StatusNoContent)
}

// republishVehicles (re)publishes the group-membership attestation for each
// given vehicle in a single detached goroutine. Best-effort: it loads each
// vehicle's current groups from the DB (post-mutation) and publishes with a
// small bounded retry; all failures are logged and swallowed (Decision 1).
//
// The tenant is passed by value (it carries the decrypted signing key) and a
// fresh background context is used because the request context is cancelled as
// soon as the handler returns.
func (fc *FleetGroupsController) republishVehicles(tenant models.Tenant, tokenIDs []int64) {
	if fc.attest == nil || len(tokenIDs) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		for _, tid := range tokenIDs {
			groups, err := fc.groups.VehicleGroups(ctx, tenant.ID, tid)
			if err != nil {
				fc.logger.Err(err).Str("tenant", tenant.ID).Int64("token_id", tid).
					Msg("load vehicle groups for attestation")
				continue
			}
			fc.attestWithRetry(ctx, tenant, tid, groups)
		}
	}()
}

// republishVehicle is the single-vehicle convenience over republishVehicles.
func (fc *FleetGroupsController) republishVehicle(tenant models.Tenant, tokenID int64) {
	fc.republishVehicles(tenant, []int64{tokenID})
}

// attestWithRetry publishes one vehicle's groups with up to 3 attempts and
// exponential backoff. Best-effort — it returns nothing; the final failure is
// logged at error level.
func (fc *FleetGroupsController) attestWithRetry(ctx context.Context, tenant models.Tenant, tokenID int64, groups []service.GroupRef) {
	const maxAttempts = 3
	backoff := time.Second
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		eventID, err := fc.attest.AttestVehicleGroups(tenant, uint64(tokenID), groups)
		if err == nil {
			fc.logger.Info().Str("tenant", tenant.ID).Int64("token_id", tokenID).
				Str("event_id", eventID).Int("groups", len(groups)).Msg("published vehicle groups attestation")
			return
		}
		fc.logger.Warn().Err(err).Str("tenant", tenant.ID).Int64("token_id", tokenID).
			Int("attempt", attempt).Msg("publish vehicle groups attestation failed")
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	fc.logger.Error().Str("tenant", tenant.ID).Int64("token_id", tokenID).
		Msg("gave up publishing vehicle groups attestation after retries")
}

// mapServiceError translates service sentinel errors into HTTP errors.
func (fc *FleetGroupsController) mapServiceError(err error, msg string) error {
	switch {
	case errors.Is(err, service.ErrGroupNotFound):
		return fiber.NewError(fiber.StatusNotFound, "fleet group not found")
	case errors.Is(err, service.ErrVehicleNotFound):
		return fiber.NewError(fiber.StatusNotFound, "vehicle not found")
	case errors.Is(err, service.ErrGroupNameExists):
		return fiber.NewError(fiber.StatusConflict, "a fleet group with this name already exists")
	default:
		fc.logger.Err(err).Msg(msg)
		return fiber.NewError(fiber.StatusInternalServerError, msg)
	}
}
