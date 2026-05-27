package controllers

import (
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

// GetWalletAddressFromJWT pulls the `ethereum_address` claim from the bearer
// JWT that the gofiber/contrib/jwt middleware stashed at c.Locals("user").
func GetWalletAddressFromJWT(c *fiber.Ctx) (common.Address, error) {
	user, ok := c.Locals("user").(*jwt.Token)
	if !ok {
		return common.Address{}, fmt.Errorf("missing JWT in context")
	}
	claims, ok := user.Claims.(jwt.MapClaims)
	if !ok {
		return common.Address{}, fmt.Errorf("invalid JWT claims")
	}
	address, ok := claims["ethereum_address"].(string)
	if !ok {
		return common.Address{}, fmt.Errorf("ethereum_address not found in claims")
	}
	return common.HexToAddress(address), nil
}

// ParseTokenIDParam pulls a uint64 tokenID out of a Fiber path parameter.
func ParseTokenIDParam(c *fiber.Ctx, paramName string) (uint64, error) {
	raw := c.Params(paramName)
	if raw == "" {
		return 0, fiber.NewError(fiber.StatusBadRequest, paramName+" is required")
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fiber.NewError(fiber.StatusBadRequest, "invalid "+paramName+" format")
	}
	return id, nil
}
