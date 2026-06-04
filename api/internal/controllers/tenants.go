package controllers

import (
	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

type TenantsController struct {
	logger       *zerolog.Logger
	settings     *config.Settings
	tenantSvc    *service.TenantService
	vehicleSvc   *service.VehicleService
	identity     gateway.IdentityAPI
	authProvider *gateway.DimoAuthProvider
}

func NewTenantsController(
	logger *zerolog.Logger,
	settings *config.Settings,
	tenantSvc *service.TenantService,
	vehicleSvc *service.VehicleService,
	identity gateway.IdentityAPI,
	authProvider *gateway.DimoAuthProvider,
) *TenantsController {
	return &TenantsController{
		logger:       logger,
		settings:     settings,
		tenantSvc:    tenantSvc,
		vehicleSvc:   vehicleSvc,
		identity:     identity,
		authProvider: authProvider,
	}
}

type tenantJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

// requireMember resolves the caller's wallet and confirms membership in the
// path tenant, returning the wallet and role.
func (t *TenantsController) requireMember(c *fiber.Ctx) (string, string, error) {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return "", "", fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	tenantID := c.Params("id")
	if tenantID == "" {
		return "", "", fiber.NewError(fiber.StatusBadRequest, "tenant id is required")
	}
	role, err := t.tenantSvc.GetMembershipRole(c.Context(), tenantID, wallet.Hex())
	if err != nil || role == "" {
		return "", "", fiber.NewError(fiber.StatusForbidden, "no access to tenant")
	}
	return wallet.Hex(), role, nil
}

// GetTenants — GET /tenants. Lists the tenants the caller is a member of.
func (t *TenantsController) GetTenants(c *fiber.Ctx) error {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	rows, err := t.tenantSvc.ListTenantsForWallet(c.Context(), wallet.Hex())
	if err != nil {
		t.logger.Err(err).Str("wallet", wallet.Hex()).Msg("list tenants")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list tenants")
	}
	out := make([]tenantJSON, 0, len(rows))
	for _, r := range rows {
		out = append(out, tenantJSON{ID: r.ID, Name: r.Name})
	}
	return c.JSON(fiber.Map{"tenants": out})
}

type createTenantRequest struct {
	Name     string `json:"name"`
	ClientID string `json:"clientId"`
	APIKey   string `json:"apiKey"`
}

// CreateTenant — POST /tenants. Validates the supplied DIMO developer creds by
// minting a developer JWT, then creates the tenant + owner membership and kicks
// off an initial vehicle sync.
func (t *TenantsController) CreateTenant(c *fiber.Ctx) error {
	wallet, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	var req createTenantRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if req.ClientID == "" || req.APIKey == "" {
		return fiber.NewError(fiber.StatusBadRequest, "clientId and apiKey are required")
	}
	name := req.Name
	if name == "" {
		name = req.ClientID
	}

	// Validate the creds before persisting: resolve the dev-license redirect URI
	// and try to mint a developer JWT.
	probe := models.Tenant{ID: "validate:" + req.ClientID, ClientID: req.ClientID, DIMOPrivateKey: req.APIKey}
	if lic, lerr := t.identity.FetchDeveloperLicenseByClientID(req.ClientID); lerr == nil && lic != nil && len(lic.RedirectURIs.Edges) > 0 {
		probe.DIMORedirectURI = lic.RedirectURIs.Edges[0].Node.URI
	}
	if _, verr := t.authProvider.GetDeveloperJWT(probe); verr != nil {
		t.logger.Warn().Err(verr).Str("client_id", req.ClientID).Msg("tenant credential validation failed")
		return fiber.NewError(fiber.StatusBadRequest, "could not authenticate with DIMO using the provided client ID and API key")
	}

	tenant, err := t.tenantSvc.CreateTenant(c.Context(), name, req.ClientID, req.APIKey, wallet.Hex())
	if err != nil {
		t.logger.Err(err).Msg("create tenant")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to create tenant")
	}

	// Best-effort initial sync so the fleet isn't empty on first load.
	if loaded, lerr := t.tenantSvc.GetTenantByID(c.Context(), tenant.ID); lerr == nil {
		if n, serr := t.vehicleSvc.SyncVehicles(c.Context(), loaded); serr != nil {
			t.logger.Warn().Err(serr).Str("tenant", tenant.ID).Msg("initial vehicle sync failed")
		} else {
			t.logger.Info().Int("count", n).Str("tenant", tenant.ID).Msg("initial vehicle sync")
		}
	}

	return c.Status(fiber.StatusCreated).JSON(tenantJSON{ID: tenant.ID, Name: tenant.Name, Role: service.RoleOwner})
}

// SyncVehicles — POST /tenants/:id/sync-vehicles. Re-syncs the tenant's vehicles.
func (t *TenantsController) SyncVehicles(c *fiber.Ctx) error {
	if _, _, err := t.requireMember(c); err != nil {
		return err
	}
	tenant, err := t.tenantSvc.GetTenantByID(c.Context(), c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load tenant")
	}
	n, err := t.vehicleSvc.SyncVehicles(c.Context(), tenant)
	if err != nil {
		t.logger.Err(err).Str("tenant", tenant.ID).Msg("sync vehicles")
		return fiber.NewError(fiber.StatusBadGateway, "vehicle sync failed: "+err.Error())
	}
	return c.JSON(fiber.Map{"synced": n})
}

type memberJSON struct {
	Wallet string `json:"wallet"`
	Role   string `json:"role"`
}

// GetMembers — GET /tenants/:id/members. Any member can list the tenant's members.
func (t *TenantsController) GetMembers(c *fiber.Ctx) error {
	if _, _, err := t.requireMember(c); err != nil {
		return err
	}
	rows, err := t.tenantSvc.ListMembers(c.Context(), c.Params("id"))
	if err != nil {
		t.logger.Err(err).Str("tenant", c.Params("id")).Msg("list members")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list members")
	}
	out := make([]memberJSON, 0, len(rows))
	for _, r := range rows {
		out = append(out, memberJSON{Wallet: r.Wallet, Role: r.Role})
	}
	return c.JSON(fiber.Map{"members": out})
}

type addMemberRequest struct {
	Wallet string `json:"wallet"`
	Role   string `json:"role"`
}

// AddMember — POST /tenants/:id/members. Owner-only; adds a wallet to the tenant.
func (t *TenantsController) AddMember(c *fiber.Ctx) error {
	_, role, err := t.requireMember(c)
	if err != nil {
		return err
	}
	if role != service.RoleOwner {
		return fiber.NewError(fiber.StatusForbidden, "only an owner can manage members")
	}
	var req addMemberRequest
	if err := c.BodyParser(&req); err != nil || req.Wallet == "" {
		return fiber.NewError(fiber.StatusBadRequest, "wallet is required")
	}
	memberRole := req.Role
	if memberRole != service.RoleOwner {
		memberRole = service.RoleMember
	}
	if err := t.tenantSvc.AddMember(c.Context(), c.Params("id"), req.Wallet, memberRole); err != nil {
		t.logger.Err(err).Msg("add member")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to add member")
	}
	return c.JSON(fiber.Map{"ok": true})
}

// RemoveMember — DELETE /tenants/:id/members/:wallet. Owner-only.
func (t *TenantsController) RemoveMember(c *fiber.Ctx) error {
	_, role, err := t.requireMember(c)
	if err != nil {
		return err
	}
	if role != service.RoleOwner {
		return fiber.NewError(fiber.StatusForbidden, "only an owner can manage members")
	}
	wallet := c.Params("wallet")
	if wallet == "" {
		return fiber.NewError(fiber.StatusBadRequest, "wallet is required")
	}
	if err := t.tenantSvc.RemoveMember(c.Context(), c.Params("id"), wallet); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to remove member")
	}
	return c.JSON(fiber.Map{"ok": true})
}

// GetSettings — GET /tenants/:id/settings. Returns non-secret config + has_* flags.
func (t *TenantsController) GetSettings(c *fiber.Ctx) error {
	if _, _, err := t.requireMember(c); err != nil {
		return err
	}
	tenant, err := t.tenantSvc.GetTenantByID(c.Context(), c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load tenant")
	}
	return c.JSON(fiber.Map{
		"id":            tenant.ID,
		"name":          tenant.Name,
		"dimoClientId":  tenant.ClientID,
		"hasDimoApiKey": tenant.DIMOPrivateKey != "",
	})
}

type updateSettingsRequest struct {
	ClientID *string `json:"clientId"`
	APIKey   *string `json:"apiKey"`
}

// UpdateSettings — PUT /tenants/:id/settings. Owner-only; updates DIMO credentials.
func (t *TenantsController) UpdateSettings(c *fiber.Ctx) error {
	_, role, err := t.requireMember(c)
	if err != nil {
		return err
	}
	if role != service.RoleOwner {
		return fiber.NewError(fiber.StatusForbidden, "only an owner can update settings")
	}
	var req updateSettingsRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if err := t.tenantSvc.UpdateCredentials(c.Context(), c.Params("id"), req.ClientID, req.APIKey); err != nil {
		t.logger.Err(err).Msg("update tenant settings")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to update settings")
	}
	return c.JSON(fiber.Map{"ok": true})
}
