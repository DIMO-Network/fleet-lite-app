package controllers

import (
	"errors"
	"strconv"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// CapManageVehicles gates acting on a vehicle the caller does not own. It is
// the same capability kaufmann's shared-account routes use for transfer,
// disconnect and delete — sharing joins that set because it is the same
// authority: the tenant's signer acting on somebody else's kernel account.
const CapManageVehicles = "manage_vehicles"

// SharingController proxies vehicle sharing to fleet-tenancy-api.
//
// It holds no signing capability of its own and makes no on-chain decision.
// What it owns is the human half of the authorization — which member is asking
// — because this app has the session and the tenancy service does not.
type SharingController struct {
	logger     *zerolog.Logger
	sharing    *service.SharingService
	vehicleSvc *service.VehicleService
	tenancy    *gateway.TenancyAPI
}

func NewSharingController(logger *zerolog.Logger, sharing *service.SharingService,
	vehicleSvc *service.VehicleService, tenancy *gateway.TenancyAPI) *SharingController {
	return &SharingController{logger: logger, sharing: sharing, vehicleSvc: vehicleSvc, tenancy: tenancy}
}

// shareRequest is the body of POST /vehicles/:tokenID/share.
type shareRequest struct {
	Grantee string `json:"grantee"`
	// DurationDays zero means indefinite, which SACD expresses as forty years.
	DurationDays int `json:"durationDays"`
}

// ShareVehicle — POST /vehicles/:tokenID/share
//
// Returns 202 and a job id; the client polls ShareStatus. The grant itself is
// made by fleet-tenancy-api, which re-runs every authorization check and then
// re-runs them once more inside its worker before spending gas.
func (s *SharingController) ShareVehicle(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}

	var body shareRequest
	if err := c.BodyParser(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	// Shape is checked here as well as upstream so an obvious typo costs a
	// round trip rather than a queued job.
	if !common.IsHexAddress(body.Grantee) {
		return fiber.NewError(fiber.StatusBadRequest, "grantee must be a wallet address")
	}
	if body.DurationDays < 0 {
		return fiber.NewError(fiber.StatusBadRequest, "durationDays must not be negative")
	}

	wallet, err := s.requireManageVehicles(c, tenant)
	if err != nil {
		return err
	}

	// The vehicle must be one of this tenant's, read through the same
	// group-scoped path as every other vehicle route. Passing the caller's
	// token id straight upstream would let a member outside the vehicle's
	// fleet group start a share the tenancy service would accept — its checks
	// are tenant-level and know nothing about our group scope.
	allowed, _ := GetAllowedGroups(c)
	if _, verr := s.vehicleSvc.GetVehicle(c.Context(), tenant, int64(tokenID), allowed); verr != nil {
		if serr := ScopeUnavailable(verr); serr != nil {
			return serr
		}
		return fiber.NewError(fiber.StatusNotFound, "vehicle not found")
	}

	jobID, err := s.tenancy.ShareVehicle(c.Context(), tenant, int64(tokenID),
		common.HexToAddress(body.Grantee).Hex(), body.DurationDays, wallet)
	if err != nil {
		return s.upstreamError(err, tenant.ID, int64(tokenID), "queue share")
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"jobId": jobID})
}

// ShareStatus — GET /vehicles/:tokenID/share/status?jobId=
//
// Success is the isSuccessful boolean the tenancy service returns, passed
// through unchanged rather than re-derived from the state string.
func (s *SharingController) ShareStatus(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := ParseTokenIDParam(c, "tokenID")
	if err != nil {
		return err
	}
	jobID, err := strconv.ParseInt(c.Query("jobId"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "jobId is required and must be a number")
	}

	status, err := s.tenancy.ShareStatus(c.Context(), tenant, int64(tokenID), jobID)
	if err != nil {
		return s.upstreamError(err, tenant.ID, int64(tokenID), "read share status")
	}
	return c.JSON(status)
}

// requireManageVehicles resolves the caller and confirms the capability.
//
// Unlike requireTenantCapability this reads the tenant from the middleware's
// context rather than an :id path parameter, because sharing lives on the
// header-scoped routes with the rest of the vehicle surface.
//
// A tenancy failure is a 503, not a 403: "we could not check" is not "you may
// not", and collapsing them would tell a member they lack a capability they
// hold whenever the upstream is briefly unavailable.
func (s *SharingController) requireManageVehicles(c *fiber.Ctx, tenant models.Tenant) (string, error) {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return "", fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	if s.tenancy == nil || !s.tenancy.Configured() {
		// No tenancy client means no signer and no share path at all, so this
		// is unavailable rather than forbidden.
		return "", fiber.NewError(fiber.StatusServiceUnavailable, "vehicle sharing is not available")
	}

	res, aerr := s.tenancy.Authz(c.Context(), tenant, wallet.Hex())
	if aerr != nil {
		s.logger.Err(aerr).Str("tenant_id", tenant.ID).Str("wallet", wallet.Hex()).
			Msg("sharing capability check unavailable")
		return "", fiber.NewError(fiber.StatusServiceUnavailable, "authorization service unavailable")
	}
	if !res.Member || res.Via != "direct" {
		return "", fiber.NewError(fiber.StatusForbidden, "no access to tenant")
	}
	if !res.HasCapability(CapManageVehicles) {
		return "", fiber.NewError(fiber.StatusForbidden, "you need the manage_vehicles capability")
	}
	return wallet.Hex(), nil
}

// upstreamError passes the tenancy service's status through where it is the
// caller's answer, rather than flattening everything into a 502.
//
// The distinction that matters: its 403 is a real policy answer the customer
// needs to read ("the owner has not authorized this tenant"), and its 503 means
// sharing is switched off in that environment. Turning either into a 502 would
// tell the customer to retry something that will never succeed.
func (s *SharingController) upstreamError(err error, tenantID string, tokenID int64, op string) error {
	var status *gateway.TenancyError
	if errors.As(err, &status) {
		switch status.StatusCode {
		case fiber.StatusForbidden, fiber.StatusNotFound, fiber.StatusConflict,
			fiber.StatusBadRequest, fiber.StatusServiceUnavailable:
			return fiber.NewError(status.StatusCode, status.Message)
		}
	}
	s.logger.Err(err).Str("tenant_id", tenantID).Int64("token_id", tokenID).Msg(op)
	return fiber.NewError(fiber.StatusBadGateway, "vehicle sharing is temporarily unavailable")
}
