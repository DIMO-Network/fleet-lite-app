package controllers

import (
	"errors"
	"regexp"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

var hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// FleetGroupsController exposes tenant-scoped CRUD over fleet groups and vehicle
// membership. All handlers run behind the tenant middleware (JWT + Tenant-Id).
// Mutations write through FleetGroupService to fleet-tenancy-api, which owns
// the record since P4; publishing any downstream view of groups is that
// service's job, not this controller's.
type FleetGroupsController struct {
	logger *zerolog.Logger
	groups *service.FleetGroupService
}

func NewFleetGroupsController(logger *zerolog.Logger, groups *service.FleetGroupService) *FleetGroupsController {
	return &FleetGroupsController{logger: logger, groups: groups}
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
	groups, err := fc.groups.ListGroupsView(c.Context(), tenant)
	if err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		fc.logger.Err(err).Str("tenant", tenant.ID).Msg("list fleet groups")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list fleet groups")
	}
	// Limited members only see their allowed groups.
	if allowed, limited := GetAllowedGroups(c); limited {
		allowedSet := toSet(allowed)
		kept := groups[:0]
		for _, g := range groups {
			if allowedSet[g.Group.ID] {
				kept = append(kept, g)
			}
		}
		groups = kept
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
	// Out-of-scope groups look nonexistent to limited members.
	if allowed, limited := GetAllowedGroups(c); limited && !toSet(allowed)[c.Params("id")] {
		return fiber.NewError(fiber.StatusNotFound, "fleet group not found")
	}
	g, members, err := fc.groups.GetGroupView(c.Context(), tenant, c.Params("id"))
	if err != nil {
		return fc.mapServiceError(err, "get fleet group")
	}
	return c.JSON(toFleetGroupResponse(g, len(members)))
}

// CreateGroup — POST /fleet/groups {name,color}
func (fc *FleetGroupsController) CreateGroup(c *fiber.Ctx) error {
	if err := RequireFullAccess(c); err != nil {
		return err
	}
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
	g, err := fc.groups.CreateGroup(c.Context(), tenant, req.Name, req.Color)
	if err != nil {
		return fc.mapServiceError(err, "create fleet group")
	}
	return c.Status(fiber.StatusCreated).JSON(toFleetGroupResponse(g, 0))
}

// UpdateGroup — PATCH /fleet/groups/:id {name?,color?}
func (fc *FleetGroupsController) UpdateGroup(c *fiber.Ctx) error {
	if err := RequireFullAccess(c); err != nil {
		return err
	}
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
	g, err := fc.groups.UpdateGroup(c.Context(), tenant, c.Params("id"), req.Name, req.Color)
	if err != nil {
		return fc.mapServiceError(err, "update fleet group")
	}
	members, _ := fc.groups.GroupMemberTokenIDs(c.Context(), tenant, g.ID)
	return c.JSON(toFleetGroupResponse(g, len(members)))
}

// DeleteGroup — DELETE /fleet/groups/:id
func (fc *FleetGroupsController) DeleteGroup(c *fiber.Ctx) error {
	if err := RequireFullAccess(c); err != nil {
		return err
	}
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if err := fc.groups.DeleteGroup(c.Context(), tenant, c.Params("id")); err != nil {
		return fc.mapServiceError(err, "delete fleet group")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// AddVehicleToGroup — POST /fleet/vehicles/:tokenID/group/:groupID
func (fc *FleetGroupsController) AddVehicleToGroup(c *fiber.Ctx) error {
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
	groupID := c.Params("groupID")
	if _, err := fc.groups.AddVehicle(c.Context(), tenant, int64(tokenID), groupID); err != nil {
		return fc.mapServiceError(err, "add vehicle to group")
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"tokenId": tokenID, "groupId": groupID})
}

// RemoveVehicleFromGroup — DELETE /fleet/vehicles/:tokenID/group/:groupID
func (fc *FleetGroupsController) RemoveVehicleFromGroup(c *fiber.Ctx) error {
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
	groupID := c.Params("groupID")
	if _, err := fc.groups.RemoveVehicle(c.Context(), tenant, int64(tokenID), groupID); err != nil {
		return fc.mapServiceError(err, "remove vehicle from group")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// SyncVehicleGroups — POST /fleet/vehicles/:tokenID/groups/sync
//
// One owner, nothing to sync: since P4 every group write goes to
// fleet-tenancy-api and the local tables mirror it, so there is no peer
// attestation stream left to pull. The endpoint survives only for the
// frontend contract — same route, same response shape, `synced` always false
// — and goes away with it.
func (fc *FleetGroupsController) SyncVehicleGroups(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	// Limited members may only see vehicles inside their allowed groups (and
	// out-of-scope vehicles look nonexistent).
	allowed, limited := GetAllowedGroups(c)
	if limited {
		ok, aerr := fc.groups.VehicleInGroups(c.Context(), tenant, int64(tokenID), allowed)
		if aerr != nil {
			if serr := ScopeUnavailable(aerr); serr != nil {
				return serr
			}
			return fiber.NewError(fiber.StatusNotFound, "vehicle not found")
		}
		if !ok {
			return fiber.NewError(fiber.StatusNotFound, "vehicle not found")
		}
	}

	groups, err := fc.groups.VehicleGroups(c.Context(), tenant, int64(tokenID))
	if err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		fc.logger.Err(err).Str("tenant", tenant.ID).Int64("token_id", int64(tokenID)).
			Msg("load vehicle groups")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load vehicle groups")
	}
	if groups == nil {
		groups = []service.GroupRef{}
	}
	// Don't leak the names of groups outside a limited member's scope.
	if limited {
		allowedSet := toSet(allowed)
		kept := groups[:0]
		for _, g := range groups {
			if allowedSet[g.ID] {
				kept = append(kept, g)
			}
		}
		groups = kept
	}
	return c.JSON(fiber.Map{"groups": groups, "synced": false, "added": 0, "removed": 0})
}

// mapServiceError translates service sentinel errors into HTTP errors.
func (fc *FleetGroupsController) mapServiceError(err error, msg string) error {
	switch {
	case errors.Is(err, service.ErrGroupScopeUnavailable):
		fc.logger.Err(err).Msg(msg)
		return fiber.NewError(fiber.StatusServiceUnavailable, "authorization service unavailable")
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
