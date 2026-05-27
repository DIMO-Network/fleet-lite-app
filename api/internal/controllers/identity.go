package controllers

import (
	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

type IdentityController struct {
	settings    *config.Settings
	logger      *zerolog.Logger
	identityAPI service.IdentityAPI
}

func NewIdentityController(settings *config.Settings, logger *zerolog.Logger) *IdentityController {
	return &IdentityController{
		settings:    settings,
		logger:      logger,
		identityAPI: service.NewIdentityAPIService(*logger, settings.IdentityAPIEndpoint.String()),
	}
}

// GetVehicleByTokenID — proxy a vehicle query to identity-api by tokenID.
func (i *IdentityController) GetVehicleByTokenID(c *fiber.Ctx) error {
	tokenID := c.Params("tokenID")
	if tokenID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "tokenID is required")
	}

	data, err := i.identityAPI.GetVehicleByTokenID(tokenID)
	if err != nil {
		i.logger.Err(err).Str("tokenID", tokenID).Msg("Failed to get vehicle by token ID")
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get vehicle information")
	}
	c.Set("Content-Type", "application/json")
	return c.Send(data)
}

// GetDefinitionByID — proxy a deviceDefinition query to identity-api by mmy id.
func (i *IdentityController) GetDefinitionByID(c *fiber.Ctx) error {
	id := c.Params("id")
	if id == "" {
		return fiber.NewError(fiber.StatusBadRequest, "definition id is required")
	}

	data, err := i.identityAPI.GetDefinitionByID(id)
	if err != nil {
		i.logger.Err(err).Str("definition_id", id).Msg("Failed to get definition by id")
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get definition information")
	}
	c.Set("Content-Type", "application/json")
	return c.Send(data)
}

// GetOwnerBy0x — proxy a paged vehicles-by-owner query to identity-api.
func (i *IdentityController) GetOwnerBy0x(c *fiber.Ctx) error {
	owner := c.Params("owner")
	if owner == "" {
		return fiber.NewError(fiber.StatusBadRequest, "wallet 0x is required")
	}

	after := c.Query("after")
	first := c.QueryInt("first", 25)

	data, err := i.identityAPI.GetOwnerBy0x(owner, first, after)
	if err != nil {
		i.logger.Err(err).Str("owner_0x", owner).Msg("Failed to get owner by 0x")
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get owner information")
	}
	c.Set("Content-Type", "application/json")
	return c.Send(data)
}

type identityProxyReq struct {
	Query string `json:"query"`
}

// ProxyGraphQLQuery — pass an arbitrary GraphQL query through to identity-api.
// Used by the frontend so it can compose richer queries without a typed contract.
func (i *IdentityController) ProxyGraphQLQuery(c *fiber.Ctx) error {
	var req identityProxyReq
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if req.Query == "" {
		return fiber.NewError(fiber.StatusBadRequest, "query is required")
	}

	data, err := i.identityAPI.Query(req.Query)
	if err != nil {
		i.logger.Err(err).Msg("Failed to proxy identity GraphQL query")
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to execute identity query")
	}
	c.Set("Content-Type", "application/json")
	return c.Send(data)
}
