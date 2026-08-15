package controllers

import (
	"database/sql"
	"errors"
	"time"

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
	// memberships answers whether enforcement is on, for /me/access. nil means
	// the feature does not exist on this deployment and reads as "off".
	memberships *service.MembershipService
	// tenancy lists a wallet's operator-managed tenants and answers membership
	// for tenants with no local tenant_users rows. nil or unconfigured means
	// local-only behaviour, exactly as before the operator-tenancy work.
	tenancy *gateway.TenancyAPI
}

func NewTenantsController(
	logger *zerolog.Logger,
	settings *config.Settings,
	tenantSvc *service.TenantService,
	vehicleSvc *service.VehicleService,
	identity gateway.IdentityAPI,
	authProvider *gateway.DimoAuthProvider,
	memberships *service.MembershipService,
	tenancy *gateway.TenancyAPI,
) *TenantsController {
	return &TenantsController{
		logger:       logger,
		settings:     settings,
		tenantSvc:    tenantSvc,
		vehicleSvc:   vehicleSvc,
		identity:     identity,
		authProvider: authProvider,
		memberships:  memberships,
		tenancy:      tenancy,
	}
}

// tenancyConfigured reports whether the shared tenancy service is reachable in
// this deployment.
func (t *TenantsController) tenancyConfigured() bool {
	return t.tenancy != nil && t.tenancy.Configured()
}

type tenantJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

// requireMember resolves the caller's wallet and confirms membership in the
// path tenant, returning the wallet and role.
//
// Local tenant_users first — the self-serve fast path — then the tenancy
// service, which is the only place an operator-managed tenant's memberships
// exist at all. The fallback answer must be a direct membership: a delegation
// is an operator's management right and never a fleet-lite session.
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
	if err == nil && role != "" {
		return wallet.Hex(), role, nil
	}

	if !t.tenancyConfigured() {
		return "", "", fiber.NewError(fiber.StatusForbidden, "no access to tenant")
	}
	tenant, terr := t.tenantSvc.GetOrMirrorTenant(c.Context(), tenantID)
	if terr != nil {
		return "", "", fiber.NewError(fiber.StatusForbidden, "no access to tenant")
	}
	res, aerr := t.tenancy.Authz(c.Context(), *tenant, wallet.Hex())
	if aerr != nil {
		// Fail closed, and as unavailability rather than refusal — the same
		// split NewTenantMiddleware makes.
		t.logger.Err(aerr).Str("tenant_id", tenantID).Str("wallet", wallet.Hex()).
			Msg("tenancy membership check unavailable")
		return "", "", fiber.NewError(fiber.StatusServiceUnavailable, "authorization service unavailable")
	}
	if !res.Member || res.Via != "direct" {
		return "", "", fiber.NewError(fiber.StatusForbidden, "no access to tenant")
	}
	return wallet.Hex(), res.Role, nil
}

// GetTenants — GET /tenants. Lists the tenants the caller is a member of: the
// union of the local list (self-serve tenants, which POST /tenants still
// writes only here) and the tenancy service's answer (operator-managed
// tenants, which have no local membership rows at all). Deduped by id — every
// backfilled tenant is in both — with the local name winning only in being
// listed first; the sets agree by construction.
//
// A tenancy failure degrades to the local list with a warning rather than
// failing the request: this is a listing, not an authorization decision —
// opening any tenant still goes through the fail-closed authz middleware.
// The degraded answer is exactly the pre-tenancy one.
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
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		out = append(out, tenantJSON{ID: r.ID, Name: r.Name})
		seen[r.ID] = true
	}

	if t.tenancyConfigured() {
		remote, rerr := t.tenancy.WalletTenants(c.Context(), wallet.Hex())
		if rerr != nil {
			t.logger.Warn().Err(rerr).Str("wallet", wallet.Hex()).
				Msg("tenancy tenant list unavailable; serving the local list only")
		}
		for _, r := range remote {
			if seen[r.TenantID] {
				continue
			}
			out = append(out, tenantJSON{ID: r.TenantID, Name: r.Name, Role: r.Role})
		}
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
	tenant, err := t.tenantSvc.GetOrMirrorTenant(c.Context(), c.Params("id"))
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
	Wallet      string  `json:"wallet"`
	Role        string  `json:"role"`
	Email       string  `json:"email,omitempty"`
	LastLoginAt *string `json:"lastLoginAt,omitempty"`
	// AllowedGroupIds is the member's group scope: absent = full access,
	// an array = limited to those fleet groups. See docs/GROUP_ACCESS_PLAN.md.
	AllowedGroupIDs []string `json:"allowedGroupIds,omitempty"`
}

// GetMembers — GET /tenants/:id/members. Any member can list the tenant's members.
//
// An operator-managed tenant's members exist only in fleet-tenancy-api, so its
// list is served from there; a self-serve tenant keeps its local list.
func (t *TenantsController) GetMembers(c *fiber.Ctx) error {
	if _, _, err := t.requireMember(c); err != nil {
		return err
	}
	if t.tenancyConfigured() {
		if tenant, terr := t.tenantSvc.GetOrMirrorTenant(c.Context(), c.Params("id")); terr == nil && tenant.ClientID == "" {
			return t.getRemoteMembers(c, *tenant)
		}
	}
	rows, err := t.tenantSvc.ListMembers(c.Context(), c.Params("id"))
	if err != nil {
		t.logger.Err(err).Str("tenant", c.Params("id")).Msg("list members")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list members")
	}
	out := make([]memberJSON, 0, len(rows))
	for _, r := range rows {
		m := memberJSON{Wallet: r.Wallet, Role: r.Role, Email: r.Email.String}
		if r.LastLoginAt.Valid {
			s := r.LastLoginAt.Time.UTC().Format(time.RFC3339)
			m.LastLoginAt = &s
		}
		// nil = full access; an array = limited to those fleet groups.
		if r.Role != service.RoleOwner && r.AllowedGroupIds != nil {
			m.AllowedGroupIDs = r.AllowedGroupIds
		}
		out = append(out, m)
	}
	return c.JSON(fiber.Map{"members": out})
}

type loginRequest struct {
	Email string `json:"email"`
}

// LoginTouch — POST /tenants/:id/login. Records the caller's last_login_at and
// email (the client supplies the email, since the DIMO JWT carries neither name
// nor email). Drives the group-sync cron's tenant-activity tiering and powers
// the Members list. Any member may touch their own login.
//
// Written to both membership stores, each best-effort once the caller is
// known to be a member: the local row (absent by design for an
// operator-managed tenant) and the shared one in fleet-tenancy-api (where the
// operator console reads last-activity from). A lost stamp is telemetry lost,
// never a failed login.
func (t *TenantsController) LoginTouch(c *fiber.Ctx) error {
	wallet, _, err := t.requireMember(c)
	if err != nil {
		return err
	}
	var req loginRequest
	_ = c.BodyParser(&req) // email is optional
	tenantID := c.Params("id")

	// sql.ErrNoRows is expected: an operator-managed tenant has no
	// tenant_users row to touch.
	if err := t.tenantSvc.TouchLogin(c.Context(), tenantID, wallet, req.Email); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.logger.Warn().Err(err).Str("tenant", tenantID).Msg("local login touch failed")
	}
	if t.tenancyConfigured() {
		if tenant, terr := t.tenantSvc.GetOrMirrorTenant(c.Context(), tenantID); terr == nil {
			if rerr := t.tenancy.LoginTouch(c.Context(), *tenant, wallet, req.Email); rerr != nil {
				t.logger.Warn().Err(rerr).Str("tenant", tenantID).Msg("tenancy login touch failed")
			}
		}
	}
	return c.JSON(fiber.Map{"ok": true})
}

// getRemoteMembers serves the member list from the shared membership records —
// the only member list an operator-managed tenant has.
func (t *TenantsController) getRemoteMembers(c *fiber.Ctx, tenant models.Tenant) error {
	members, err := t.tenancy.Members(c.Context(), tenant)
	if err != nil {
		t.logger.Err(err).Str("tenant", tenant.ID).Msg("list tenancy members")
		return fiber.NewError(fiber.StatusServiceUnavailable, "member list unavailable")
	}
	out := make([]memberJSON, 0, len(members))
	for _, m := range members {
		mj := memberJSON{Wallet: m.Wallet, Role: m.Role, LastLoginAt: m.LastLoginAt}
		if m.Email != nil {
			mj.Email = *m.Email
		}
		// Same three-valued scope encoding the local list uses: absent means
		// full access, an array (even empty) means limited to those groups.
		if m.Role != service.RoleOwner && m.ScopeGroupIDs != nil {
			mj.AllowedGroupIDs = m.ScopeGroupIDs
		}
		out = append(out, mj)
	}
	return c.JSON(fiber.Map{"members": out})
}

type addMemberRequest struct {
	Wallet string `json:"wallet"`
	Role   string `json:"role"`
	// AllowedGroupIds limits the member to those fleet groups; omit/null for
	// full access. Ignored for owner role.
	AllowedGroupIDs []string `json:"allowedGroupIds"`
}

// AddMember — POST /tenants/:id/members. Requires manage_members; writes the
// membership locally AND through to the tenancy service, which is where authz
// answers come from — a local-only row would report success and confer nothing.
func (t *TenantsController) AddMember(c *fiber.Ctx) error {
	actor, err := requireTenantCapability(c, t.logger, t.tenantSvc, t.tenancy, CapManageMembers)
	if err != nil {
		return err
	}
	var req addMemberRequest
	if err := c.BodyParser(&req); err != nil || req.Wallet == "" {
		return fiber.NewError(fiber.StatusBadRequest, "wallet is required")
	}
	memberRole := req.Role
	if memberRole != service.RoleOwner {
		memberRole = service.RoleMember
	}
	tenant, err := t.tenantSvc.GetOrMirrorTenant(c.Context(), c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load tenant")
	}
	if err := t.tenantSvc.GrantMember(c.Context(), tenant, req.Wallet, memberRole, req.AllowedGroupIDs, actor); err != nil {
		return t.mapMemberWriteError(err, "add member")
	}
	return c.JSON(fiber.Map{"ok": true})
}

// mapMemberWriteError keeps the split the write-through creates: an upstream
// failure is a 502 the caller should retry, anything else is this app's fault.
func (t *TenantsController) mapMemberWriteError(err error, op string) error {
	t.logger.Err(err).Msg(op)
	if errors.Is(err, service.ErrTenancyWriteFailed) {
		return fiber.NewError(fiber.StatusBadGateway, "membership write did not reach the tenancy service; retry")
	}
	return fiber.NewError(fiber.StatusInternalServerError, "failed to "+op)
}

type updateMemberAccessRequest struct {
	// AllowedGroupIds: null = full access, array = limited to those groups.
	AllowedGroupIDs []string `json:"allowedGroupIds"`
}

// UpdateMemberAccess — PUT /tenants/:id/members/:wallet/access. Requires
// manage_members; changes an existing member's allowed fleet groups (null =
// full access), locally and in the shared model.
func (t *TenantsController) UpdateMemberAccess(c *fiber.Ctx) error {
	_, err := requireTenantCapability(c, t.logger, t.tenantSvc, t.tenancy, CapManageMembers)
	if err != nil {
		return err
	}
	var req updateMemberAccessRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	tenant, err := t.tenantSvc.GetOrMirrorTenant(c.Context(), c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load tenant")
	}
	if err := t.tenantSvc.ChangeMemberScope(c.Context(), tenant, c.Params("wallet"), req.AllowedGroupIDs); err != nil {
		if errors.Is(err, service.ErrTenancyWriteFailed) {
			return t.mapMemberWriteError(err, "update member access")
		}
		t.logger.Err(err).Str("tenant", c.Params("id")).Msg("update member access")
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true})
}

// GetMyAccess — GET /me/access (tenant-scoped). Returns the caller's own role
// and group scope so any view can cheaply render/gate by it.
// allowedGroupIds is null for unrestricted callers (owners, full members).
//
// membershipsEnforced rides along so the frontend can explain an empty garage
// — "your operator has memberships switched on and none are active" — instead
// of looking broken. Best-effort, defaulting to FALSE on failure: this field
// only ever softens the message, never gates access, and the vehicle list
// itself fails closed through membershipFilter. Reporting false while the
// list 503s is a stale caption; reporting a 500 here would break every view
// that loads access state.
func (t *TenantsController) GetMyAccess(c *fiber.Ctx) error {
	role := GetTenantRole(c)

	enforced := false
	if t.memberships != nil && t.memberships.Configured() {
		if tenant, terr := GetTenant(c); terr == nil {
			e, _, merr := t.memberships.ActiveTokens(c.Context(), tenant)
			if merr != nil {
				t.logger.Warn().Err(merr).Str("tenant", tenant.ID).
					Msg("membership state unavailable for /me/access; reporting unenforced")
			} else {
				enforced = e
			}
		}
	}

	allowed, limited := GetAllowedGroups(c)
	if !limited {
		return c.JSON(fiber.Map{"role": role, "allowedGroupIds": nil, "membershipsEnforced": enforced})
	}
	return c.JSON(fiber.Map{"role": role, "allowedGroupIds": allowed, "membershipsEnforced": enforced})
}

// RemoveMember — DELETE /tenants/:id/members/:wallet. Owner-only.
func (t *TenantsController) RemoveMember(c *fiber.Ctx) error {
	_, err := requireTenantCapability(c, t.logger, t.tenantSvc, t.tenancy, CapManageMembers)
	if err != nil {
		return err
	}
	wallet := c.Params("wallet")
	if wallet == "" {
		return fiber.NewError(fiber.StatusBadRequest, "wallet is required")
	}
	tenant, err := t.tenantSvc.GetOrMirrorTenant(c.Context(), c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load tenant")
	}
	// Revocations go to the shared model FIRST: if the local delete then
	// fails, authz already refuses — the person can only ever end up with
	// less access than intended, never more.
	if err := t.tenantSvc.RemoveMemberEverywhere(c.Context(), tenant, wallet); err != nil {
		return t.mapMemberWriteError(err, "remove member")
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

// UpdateSettings — PUT /tenants/:id/settings. Requires manage_settings;
// updates DIMO credentials. Refused outright for an operator-managed tenant:
// its effective credential is the operator's license, held by the tenancy
// service, and a locally-written key would silently fork it.
func (t *TenantsController) UpdateSettings(c *fiber.Ctx) error {
	_, err := requireTenantCapability(c, t.logger, t.tenantSvc, t.tenancy, CapManageSettings)
	if err != nil {
		return err
	}
	if t.tenancyConfigured() {
		if tenant, terr := t.tenantSvc.GetOrMirrorTenant(c.Context(), c.Params("id")); terr == nil && tenant.ClientID == "" {
			return fiber.NewError(fiber.StatusConflict, "this fleet's credentials are managed by your operator")
		}
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
