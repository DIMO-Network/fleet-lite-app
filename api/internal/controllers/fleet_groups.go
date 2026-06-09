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
	return c.JSON(toFleetGroupResponse(g, len(members)))
}

// DeleteGroup — DELETE /fleet/groups/:id
func (fc *FleetGroupsController) DeleteGroup(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if err := fc.groups.DeleteGroup(c.Context(), tenant.ID, c.Params("id")); err != nil {
		return fc.mapServiceError(err, "delete fleet group")
	}
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
	return c.SendStatus(fiber.StatusNoContent)
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
