package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/ethereum/go-ethereum/common"
	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog"
)

// TenancyAPI calls fleet-tenancy-api, the shared source of truth for tenants,
// users, memberships, delegations and vehicle entitlements.
//
// Nothing in this app's request path calls it yet. It exists so the cutover
// from our own tenants/tenant_users tables is a change of call sites rather
// than a change of plumbing; `tenancy-check` exercises it end to end today.
//
// AUTHENTICATION IS TWO HEADERS, ANSWERING TWO DIFFERENT QUESTIONS.
//
//	X-Tenancy-Key   is this a trusted application?    (pre-shared, per app)
//	Authorization   which tenant is it acting as?     (developer-license JWT)
//
// The key alone reaches nothing: the service resolves the JWT's client id to a
// tenant and then bounds the request to tenants that credential reaches. Since
// we mint the JWT from the *subject* tenant's own developer license, our caller
// identity and the tenant we ask about are the same, and the service's scope
// check passes on its "self" branch.
//
// A consequence worth remembering at cutover: we can only ask about a tenant
// whose credentials we hold. That is every tenant today, because all of them
// are unparented and self-serve. It stops being true for operator-managed
// customers, which hold no license of their own and are reached with their
// operator's — at which point the JWT to present is the operator's, not the
// customer's.
type TenancyAPI struct {
	logger       zerolog.Logger
	authProvider developerJWTProvider
	baseURL      string
	apiKey       string
	client       *http.Client

	// selfTenant is this app's own identity — the Login-with-DIMO license,
	// whose key lives in this app's secret — used to authenticate calls about
	// tenants whose credentials we do not hold: operator-managed customers.
	// The service knows the same client id as a registered service caller, so
	// scope passes on its service branch rather than the self branch.
	//
	// nil when DIMO_AUTH_CLIENT_ID / DIMO_AUTH_PRIVATE_KEY are unset (local
	// dev): calls about credential-less tenants then fail with
	// ErrAppIdentityNotConfigured rather than an unexplained mint error.
	selfTenant *models.Tenant

	// authzCache holds answers for the lifetime the service says they are good
	// for. NewTenantMiddleware asks on every request, so without this the
	// tenancy service takes this app's entire request rate.
	//
	// The cost is that revocation is eventually consistent by up to that window:
	// remove a member and they keep their access for the remainder of it. The
	// service sets the window and documents the same tradeoff; it is stated here
	// too because this is where someone debugging "I removed them and they could
	// still get in" will end up.
	authzCache *cache.Cache
}

// defaultAuthzTTL is used when the service does not say. It matches the
// service's own DefaultAuthzCacheTTLSeconds.
const defaultAuthzTTL = 60 * time.Second

// developerJWTProvider is the slice of DimoAuthProvider this client needs. Kept
// as an interface so the header and error-classification behaviour is testable
// without a real DIMO auth exchange.
type developerJWTProvider interface {
	GetDeveloperJWT(tenant models.Tenant) (string, error)
}

// tenancyTimeout bounds a single call. The service is in-cluster with no
// ingress in front of it and answers /v1/authz from two indexed reads, so a
// slow response means something is wrong rather than something is busy.
const tenancyTimeout = 5 * time.Second

// ErrTenancyNotConfigured means TENANCY_API_URL or TENANCY_API_KEY is empty.
// Returned before any request is built, so a misconfigured deployment fails
// with the reason rather than with an unexplained 401.
var ErrTenancyNotConfigured = errors.New("tenancy api is not configured")

// ErrAppIdentityNotConfigured means a call needed this app's own identity —
// the subject tenant holds no credentials — but DIMO_AUTH_CLIENT_ID or
// DIMO_AUTH_PRIVATE_KEY is unset.
var ErrAppIdentityNotConfigured = errors.New(
	"tenant holds no credentials and the app identity (DIMO_AUTH_CLIENT_ID/DIMO_AUTH_PRIVATE_KEY) is not configured")

// AccessLayer names which of the service's three access-control layers turned
// a call away.
//
// This distinction is the reason the client parses error bodies at all: layers
// 1 and 2 both answer 401, so the status code cannot tell "our key is wrong"
// from "our JWT is wrong". Without this, diagnosing a failed caller means
// reading the *service's* logs for the absence of an "unrecognised
// trusted-caller key" warning — an inference from a log line that isn't there.
type AccessLayer string

const (
	// LayerTrustedCallerKey — X-Tenancy-Key missing or unrecognised. Our key
	// does not match the service's TRUSTED_CALLER_KEYS entry for this app.
	LayerTrustedCallerKey AccessLayer = "trusted-caller-key"
	// LayerDeveloperJWT — the key passed, the JWT did not: absent, expired,
	// not verifiable against the DIMO JWKS, or its client id is registered to
	// no tenant.
	LayerDeveloperJWT AccessLayer = "developer-license-jwt"
	// LayerCallerScope — both credentials are good, but the tenant we
	// authenticated as may not ask about the tenant we asked about.
	LayerCallerScope AccessLayer = "caller-scope"
	// LayerNone — the failure was not an access-control rejection.
	LayerNone AccessLayer = ""
)

// TenancyError is a non-2xx response from the service, carrying the layer that
// rejected it where that is knowable.
type TenancyError struct {
	StatusCode int
	Message    string
	Layer      AccessLayer
}

// HTTPStatus exposes the status code behind an interface the service layer can
// errors.As against without importing this package's concrete type.
func (e *TenancyError) HTTPStatus() int { return e.StatusCode }

func (e *TenancyError) Error() string {
	if e.Layer != LayerNone {
		return fmt.Sprintf("tenancy api %d (%s): %s", e.StatusCode, e.Layer, e.Message)
	}
	return fmt.Sprintf("tenancy api %d: %s", e.StatusCode, e.Message)
}

// AuthzResult mirrors the service's response to GET /v1/authz. Keep it in step
// with fleet-tenancy-api's models.AuthzResult.
type AuthzResult struct {
	TenantID string `json:"tenantId"`
	Wallet   string `json:"wallet"`
	Member   bool   `json:"member"`
	Role     string `json:"role,omitempty"`

	// Via is "direct", "delegation" or "none". A delegated answer must not
	// grant a fleet-lite session: operator staff are b2b-only and there is no
	// impersonation. Callers here check Via, not just Member.
	Via string `json:"via"`

	// Permissions is authoritative for authorization decisions. Role is a
	// display label and a preset for adding members — never gate on it.
	Permissions []string `json:"permissions"`

	// ScopeGroupIDs nil means unrestricted; a slice restricts to those fleet
	// groups, and an *empty* slice restricts to nothing at all.
	//
	// The nil/empty distinction is load-bearing and inverted from intuition —
	// treating a decoded `[]` as "no restriction" hands the whole fleet to a
	// member who should see none of it. The backfill made exactly this mistake
	// against 131 memberships. Use Unrestricted rather than testing len().
	ScopeGroupIDs []string `json:"scopeGroupIds"`

	OperatorTenantID string `json:"operatorTenantId,omitempty"`
	TenantStatus     string `json:"tenantStatus"`

	// CacheTTLSeconds is how long the service says this answer may be reused.
	// Nothing caches it yet; when the hot path starts calling this on every
	// request, cache on (tenant, wallet) and honour this value — the cost is
	// that revocation becomes eventually consistent by up to that window.
	CacheTTLSeconds int `json:"cacheTtlSeconds"`
}

// HasCapability reports whether the result grants a capability.
func (a *AuthzResult) HasCapability(c string) bool {
	for _, p := range a.Permissions {
		if p == c {
			return true
		}
	}
	return false
}

// Unrestricted reports whether the wallet sees every group in the tenant.
func (a *AuthzResult) Unrestricted() bool { return a.ScopeGroupIDs == nil }

func NewTenancyAPI(logger zerolog.Logger, settings *config.Settings, authProvider *DimoAuthProvider) *TenancyAPI {
	t := &TenancyAPI{
		logger:       logger,
		authProvider: authProvider,
		baseURL:      strings.TrimSuffix(settings.TenancyAPIURL.String(), "/"),
		apiKey:       settings.TenancyAPIKey,
		client:       &http.Client{Timeout: tenancyTimeout},
		authzCache:   cache.New(defaultAuthzTTL, 2*defaultAuthzTTL),
	}
	if self := AppSelfTenant(settings); self != nil {
		t.selfTenant = self
	}
	return t
}

// AppSelfTenant builds the models.Tenant this app authenticates as when acting
// in its own name — the Login-with-DIMO license from settings. nil when either
// half is missing, so callers degrade with a named error instead of minting
// with a zero client id.
//
// The ID is a fixed sentinel, not a database id: it keys the auth provider's
// per-tenant caches, and it must never collide with a real tenant uuid.
func AppSelfTenant(settings *config.Settings) *models.Tenant {
	zero := common.Address{}
	if settings.DimoAuthClientID == zero || settings.DimoAuthPrivateKey == "" {
		return nil
	}
	return &models.Tenant{
		ID:              "app:fleet-lite",
		Name:            "fleet-lite-app",
		ClientID:        settings.DimoAuthClientID.Hex(),
		DIMOPrivateKey:  settings.DimoAuthPrivateKey,
		DIMORedirectURI: settings.DimoAuthRedirectURL.String(),
	}
}

// authTenant picks the identity a call about `subject` authenticates as: the
// subject itself when we hold its credentials (self-serve tenants — the scope
// check then passes on its self branch), otherwise this app's own identity
// (operator-managed tenants — the service-caller branch).
//
// The split is deliberate rather than always using the app identity: the
// bounded per-tenant path stays bounded where it can be, and every existing
// call keeps its behaviour, so a bad service-caller registration can only
// break the managed-tenant path it was added for.
func (t *TenancyAPI) authTenant(subject models.Tenant) (models.Tenant, error) {
	if subject.ClientID != "" {
		return subject, nil
	}
	if t.selfTenant == nil {
		return models.Tenant{}, ErrAppIdentityNotConfigured
	}
	return *t.selfTenant, nil
}

// authzCacheKey identifies one answer. The wallet is lowercased because the
// question "may this wallet act in this tenant" does not depend on casing, and
// the two source systems disagree about it — one person must not occupy two
// cache entries with potentially different answers.
func authzCacheKey(tenantID, wallet string) string {
	return tenantID + ":" + strings.ToLower(wallet)
}

// Configured reports whether both the URL and the key are set. Call sites that
// are meant to degrade rather than fail (a shadow read, a background job) can
// check this instead of interpreting an error.
func (t *TenancyAPI) Configured() bool { return t.baseURL != "" && t.apiKey != "" }

// Authz answers "what may this wallet do in this tenant?" — the question that
// replaces our own NewTenantMiddleware membership lookup at cutover.
//
// A wallet with no access is a 200 with Via "none", not an error. That is the
// service's contract and it matters: an error would be indistinguishable from
// the service rejecting *our* credentials, and the caller owns the status code
// its own surface returns.
func (t *TenancyAPI) Authz(ctx context.Context, tenant models.Tenant, wallet string) (*AuthzResult, error) {
	if wallet == "" {
		return nil, fmt.Errorf("wallet is required")
	}

	key := authzCacheKey(tenant.ID, wallet)
	if cached, found := t.authzCache.Get(key); found {
		return cached.(*AuthzResult), nil
	}

	q := url.Values{}
	q.Set("wallet", wallet)
	q.Set("tenant_id", tenant.ID)

	var res AuthzResult
	if err := t.get(ctx, tenant, "/v1/authz?"+q.Encode(), &res); err != nil {
		return nil, err
	}

	// Only successes are cached. Caching a rejection would extend an outage or
	// a stale key past its cause — the failure would outlive the fix.
	t.authzCache.Set(key, &res, authzTTL(res.CacheTTLSeconds))
	return &res, nil
}

// VehicleGroups returns the tenant's whole fleet-group structure — every group
// with its member token ids — from GET /v1/tenants/{id}/vehicle-groups.
//
// This is the read path of the groups move: when GROUPS_FROM_TENANCY is on,
// FleetGroupService serves its whole read surface from this call.
//
// Uncached HERE, unlike Authz — but no longer uncached. Through P3 the premise
// was that group reads are screen-shaped rather than per-request; P5 put the
// vehicle scope filter on them, so they are per-request now, and
// FleetGroupService caches the derived index. The cache is there rather than
// here because only that service sees the group writes that must bust it.
func (t *TenancyAPI) VehicleGroups(ctx context.Context, tenant models.Tenant) ([]models.RemoteFleetGroup, error) {
	var res struct {
		Groups []models.RemoteFleetGroup `json:"groups"`
	}
	if err := t.get(ctx, tenant, "/v1/tenants/"+url.PathEscape(tenant.ID)+"/vehicle-groups", &res); err != nil {
		return nil, err
	}
	return res.Groups, nil
}

// ActiveVehicleMemberships is the membership gate read: whether enforcement is
// on for this tenant, and the token ids currently paid for. On the vehicle-list
// hot path once enforcement is live, so it is cached by MembershipService —
// there rather than here for the same reason the group index cache lives in
// FleetGroupService.
func (t *TenancyAPI) ActiveVehicleMemberships(ctx context.Context, tenant models.Tenant) (*models.RemoteActiveMemberships, error) {
	var res models.RemoteActiveMemberships
	if err := t.get(ctx, tenant, "/v1/tenants/"+url.PathEscape(tenant.ID)+"/active-vehicle-memberships", &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// VehicleMemberships is the display list for the memberships page —
// screen-shaped, so uncached.
func (t *TenancyAPI) VehicleMemberships(ctx context.Context, tenant models.Tenant) (*models.RemoteMembershipList, error) {
	var res models.RemoteMembershipList
	if err := t.get(ctx, tenant, "/v1/tenants/"+url.PathEscape(tenant.ID)+"/vehicle-memberships", &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ----- The operator-tenancy surface (plan 03) -----
//
// These are what let this app open a tenant it holds no credentials for: the
// wallet's tenant list before any Tenant-Id exists, the tenant's detail and
// minted token, and the entitled vehicle set that replaces the privileged
// query for explicit-mode tenants. All of them authenticate via authTenant,
// which for a credential-less subject means this app's own identity.

// asSelf is the subject stand-in for calls that have no tenant credentials by
// construction — either no tenant is involved yet (the wallet listing) or only
// an id is known. authTenant resolves it to the app identity.
func asSelf(tenantID string) models.Tenant { return models.Tenant{ID: tenantID} }

// WalletTenants lists the tenants a wallet holds a direct membership in, from
// GET /v1/tenants?wallet=&surface=fleet_lite — already filtered to active,
// fleet-lite-visible tenants by the service.
func (t *TenancyAPI) WalletTenants(ctx context.Context, wallet string) ([]models.RemoteWalletTenant, error) {
	if wallet == "" {
		return nil, fmt.Errorf("wallet is required")
	}
	q := url.Values{}
	q.Set("wallet", wallet)
	q.Set("surface", "fleet_lite")
	var res []models.RemoteWalletTenant
	if err := t.get(ctx, asSelf(""), "/v1/tenants?"+q.Encode(), &res); err != nil {
		return nil, err
	}
	return res, nil
}

// TenantDetail reads one tenant's record from GET /v1/tenants/{id} — the
// existence check and entitlement-mode read behind the middleware's mirror
// fallback and the entitlement sync.
func (t *TenancyAPI) TenantDetail(ctx context.Context, tenantID string) (*models.RemoteTenantDetail, error) {
	var res models.RemoteTenantDetail
	if err := t.get(ctx, asSelf(tenantID), "/v1/tenants/"+url.PathEscape(tenantID), &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// DimoToken mints a developer JWT for the tenant's EFFECTIVE credential from
// GET /v1/tenants/{id}/dimo-token — the operator's license for a managed
// customer. Uncached here: DimoAuthProvider owns the cache, keyed by tenant
// and bounded by the token's own expiry.
func (t *TenancyAPI) DimoToken(ctx context.Context, tenantID string) (*models.RemoteMintedToken, error) {
	var res models.RemoteMintedToken
	if err := t.get(ctx, asSelf(tenantID), "/v1/tenants/"+url.PathEscape(tenantID)+"/dimo-token", &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// Entitlements lists the vehicles an explicit-mode tenant may see, from
// GET /v1/tenants/{id}/vehicles. The service answers 200 [] for implicit-mode
// tenants too — indistinguishable from "entitled to nothing" — so callers must
// check entitlementMode before treating an empty list as an empty fleet.
func (t *TenancyAPI) Entitlements(ctx context.Context, tenant models.Tenant) ([]models.RemoteEntitlement, error) {
	var res []models.RemoteEntitlement
	if err := t.get(ctx, tenant, "/v1/tenants/"+url.PathEscape(tenant.ID)+"/vehicles", &res); err != nil {
		return nil, err
	}
	return res, nil
}

// Members lists the shared membership records from GET /v1/tenants/{id}/members
// — for an operator-managed tenant, the only member list there is.
func (t *TenancyAPI) Members(ctx context.Context, tenant models.Tenant) ([]models.RemoteMember, error) {
	var res []models.RemoteMember
	if err := t.get(ctx, tenant, "/v1/tenants/"+url.PathEscape(tenant.ID)+"/members", &res); err != nil {
		return nil, err
	}
	return res, nil
}

// CreateSelfServeTenant registers a new self-serve tenant with the tenancy
// service — tenant, credential and owner membership in one transaction on the
// far side — and returns the record, whose id the caller MUST reuse for its
// local row. Always the app identity: the tenant does not exist yet, so there
// is no subject to authenticate as.
func (t *TenancyAPI) CreateSelfServeTenant(ctx context.Context, name, clientID, apiKey, ownerWallet, ownerEmail string) (*models.RemoteTenantDetail, error) {
	var res models.RemoteTenantDetail
	if err := t.do(ctx, asSelf(""), http.MethodPost, "/v1/tenants", map[string]string{
		"name":        name,
		"clientId":    clientID,
		"apiKey":      apiKey,
		"ownerWallet": ownerWallet,
		"ownerEmail":  ownerEmail,
	}, &res); err != nil {
		return nil, err
	}
	if res.ID == "" {
		return nil, fmt.Errorf("tenancy created a tenant but answered no id")
	}
	return &res, nil
}

// PutTenantCredentials sets or replaces a tenant's own license in the tenancy
// service. Deliberately the app identity rather than the subject's: a rotation
// must not depend on the credential being rotated away still minting.
func (t *TenancyAPI) PutTenantCredentials(ctx context.Context, tenantID, clientID, apiKey string) error {
	return t.do(ctx, asSelf(""), http.MethodPut,
		"/v1/tenants/"+url.PathEscape(tenantID)+"/credentials",
		map[string]string{"clientId": clientID, "apiKey": apiKey}, nil)
}

// RemoteMemberWrite is the body of PUT /v1/tenants/{id}/members/{wallet}. The
// service REPLACES the membership with it, so senders must carry the whole
// record — see the read-modify-write in TenantService's write-through.
type RemoteMemberWrite struct {
	Email       string
	Role        string
	Permissions []string
	// ScopeGroupIDs nil means unrestricted; an empty, non-nil slice means
	// restricted to nothing. The service refuses an absent field, so the
	// marshalling below always sends it — null or an array, never omitted.
	ScopeGroupIDs   []string
	GrantedByWallet string
}

// PutMember writes one whole membership through to the shared model — the
// write that makes a fleet-lite grant actually confer access, since authz
// answers come from the tenancy service.
func (t *TenancyAPI) PutMember(ctx context.Context, tenant models.Tenant, wallet string, w RemoteMemberWrite) error {
	perms := w.Permissions
	if perms == nil {
		perms = []string{}
	}
	body := map[string]any{
		"role":        w.Role,
		"permissions": perms,
	}
	// A map value of nil marshals as an explicit null — "unrestricted", which
	// the service must see spelled out rather than guessed at.
	if w.ScopeGroupIDs == nil {
		body["scopeGroupIds"] = nil
	} else {
		body["scopeGroupIds"] = w.ScopeGroupIDs
	}
	if w.Email != "" {
		body["email"] = w.Email
	}
	if w.GrantedByWallet != "" {
		body["grantedByWallet"] = w.GrantedByWallet
	}
	return t.do(ctx, tenant, http.MethodPut,
		"/v1/tenants/"+url.PathEscape(tenant.ID)+"/members/"+url.PathEscape(wallet), body, nil)
}

// DeleteMember removes the shared membership. Deleting one already gone is a
// 204 on the service side, so retries converge.
func (t *TenancyAPI) DeleteMember(ctx context.Context, tenant models.Tenant, wallet string) error {
	return t.do(ctx, tenant, http.MethodDelete,
		"/v1/tenants/"+url.PathEscape(tenant.ID)+"/members/"+url.PathEscape(wallet), nil, nil)
}

// LoginTouch stamps the shared membership's last_login_at via
// POST /v1/tenants/{id}/members/{wallet}/login. Telemetry, not authorization —
// callers treat a failure as a lost stamp, not a failed login.
func (t *TenancyAPI) LoginTouch(ctx context.Context, tenant models.Tenant, wallet, email string) error {
	return t.do(ctx, tenant, http.MethodPost,
		"/v1/tenants/"+url.PathEscape(tenant.ID)+"/members/"+url.PathEscape(wallet)+"/login",
		map[string]string{"email": email}, nil)
}

// ----- Group writes (P4 of the groups move) -----
//
// Every group mutation writes through to fleet-tenancy-api, which owns the
// record; the local tables are a synchronously-maintained mirror until P5
// drops them. Status mapping is left to the caller via TenancyError — the
// service layer decides what a 409 or 404 means for its own idempotency rules.

// CreateGroup creates a group; the service mints the id from the name with the
// same slug rules this app uses, so both sides land on one id.
func (t *TenancyAPI) CreateGroup(ctx context.Context, tenant models.Tenant, name, color string) error {
	return t.do(ctx, tenant, http.MethodPost,
		"/v1/tenants/"+url.PathEscape(tenant.ID)+"/groups",
		map[string]string{"name": name, "color": color}, nil)
}

// UpdateGroup renames and/or recolours. Nil fields stay untouched.
func (t *TenancyAPI) UpdateGroup(ctx context.Context, tenant models.Tenant, groupID string, name, color *string) error {
	body := map[string]string{}
	if name != nil && *name != "" {
		body["name"] = *name
	}
	if color != nil && *color != "" {
		body["color"] = *color
	}
	return t.do(ctx, tenant, http.MethodPatch,
		"/v1/tenants/"+url.PathEscape(tenant.ID)+"/groups/"+url.PathEscape(groupID), body, nil)
}

// DeleteGroup deletes a group; memberships cascade on the service side.
func (t *TenancyAPI) DeleteGroup(ctx context.Context, tenant models.Tenant, groupID string) error {
	return t.do(ctx, tenant, http.MethodDelete,
		"/v1/tenants/"+url.PathEscape(tenant.ID)+"/groups/"+url.PathEscape(groupID), nil, nil)
}

// AddGroupVehicles adds token ids to a group. Idempotent on the service side.
func (t *TenancyAPI) AddGroupVehicles(ctx context.Context, tenant models.Tenant, groupID string, tokenIDs []int64) error {
	return t.do(ctx, tenant, http.MethodPost,
		"/v1/tenants/"+url.PathEscape(tenant.ID)+"/groups/"+url.PathEscape(groupID)+"/vehicles",
		map[string][]int64{"tokenIds": tokenIDs}, nil)
}

// RemoveGroupVehicle removes one token id from a group. Removing one already
// gone succeeds on the service side.
func (t *TenancyAPI) RemoveGroupVehicle(ctx context.Context, tenant models.Tenant, groupID string, tokenID int64) error {
	return t.do(ctx, tenant, http.MethodDelete,
		"/v1/tenants/"+url.PathEscape(tenant.ID)+"/groups/"+url.PathEscape(groupID)+
			"/vehicles/"+strconv.FormatInt(tokenID, 10), nil, nil)
}

// ----- Invitations (P2 of the invitations move) -----
//
// Behind INVITES_FROM_TENANCY, the whole invitation lifecycle moves to
// fleet-tenancy-api: it mints the token, sends the email, receives Postmark's
// delivery webhooks, and — on accept — writes the membership itself.
//
// That last point is why Accept here is not "the remote call plus our usual
// grant": the tenancy accept marks the invitation and upserts the membership
// in ONE transaction, so calling GrantMember afterwards would be a second,
// weaker write of something already done.
//
// The token is deliberately absent from every request and response below. It
// exists in the invitee's email and in that service's memory at mint time,
// and nowhere else — including our logs.

// Invitations lists a tenant's invitations from
// GET /v1/tenants/{id}/invitations.
func (t *TenancyAPI) Invitations(ctx context.Context, tenant models.Tenant) ([]models.RemoteInvitation, error) {
	var res struct {
		Invitations []models.RemoteInvitation `json:"invitations"`
	}
	if err := t.get(ctx, tenant, "/v1/tenants/"+url.PathEscape(tenant.ID)+"/invitations", &res); err != nil {
		return nil, err
	}
	return res.Invitations, nil
}

// RemoteInvitationCreate is the body of POST /v1/tenants/{id}/invitations.
type RemoteInvitationCreate struct {
	Email  string
	Role   string
	Locale string
	// ScopeGroupIDs carries the tri-state: nil unrestricted, empty non-nil
	// restricted to nothing. The service refuses the field absent, so the
	// marshalling below always sends it — null or an array, never omitted.
	ScopeGroupIDs   []string
	InvitedByWallet string
}

// CreateInvitation issues an invitation: the service mints the token, stores
// its hash and emails the accept link.
//
// A 201 whose EmailSent is false is a PARTIAL SUCCESS, not a failure — the
// record is authoritative and the email is courtesy. Callers surface it as
// "created, email did not go out" and offer a resend, exactly as the local
// flow did with ErrEmailNotSent.
func (t *TenancyAPI) CreateInvitation(ctx context.Context, tenant models.Tenant, in RemoteInvitationCreate) (*models.RemoteInvitation, error) {
	body := map[string]any{
		"email":           in.Email,
		"role":            in.Role,
		"locale":          in.Locale,
		"invitedByWallet": in.InvitedByWallet,
	}
	// A map value of nil marshals as an explicit null — "unrestricted", which
	// the service must see spelled out rather than guessed at.
	if in.ScopeGroupIDs == nil {
		body["scopeGroupIds"] = nil
	} else {
		body["scopeGroupIds"] = in.ScopeGroupIDs
	}
	var res models.RemoteInvitation
	if err := t.do(ctx, tenant, http.MethodPost,
		"/v1/tenants/"+url.PathEscape(tenant.ID)+"/invitations", body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// RevokeInvitation revokes a pending invitation. Idempotent on the service
// side — revoking one already dead is a 204 — so retries converge.
func (t *TenancyAPI) RevokeInvitation(ctx context.Context, tenant models.Tenant, invitationID string) error {
	return t.do(ctx, tenant, http.MethodDelete,
		"/v1/tenants/"+url.PathEscape(tenant.ID)+"/invitations/"+url.PathEscape(invitationID), nil, nil)
}

// ResendInvitation mints a FRESH token and re-sends the email. The previous
// link dies — that is the contract, not a side effect — so an accept racing a
// resend loses.
func (t *TenancyAPI) ResendInvitation(ctx context.Context, tenant models.Tenant, invitationID, actorWallet, locale string) (*models.RemoteInvitation, error) {
	var res models.RemoteInvitation
	if err := t.do(ctx, tenant, http.MethodPost,
		"/v1/tenants/"+url.PathEscape(tenant.ID)+"/invitations/"+url.PathEscape(invitationID)+"/resend",
		map[string]string{"actorWallet": actorWallet, "locale": locale}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// AcceptInvitation consumes a token and binds the wallet to whatever tenant
// the token resolves to — which is why there is no tenant in the path, and why
// this authenticates as the app identity: the accepting wallet is not yet a
// member of anything, and we do not know the tenant until the service tells us.
//
// The service writes the membership as part of the same transaction, so the
// caller must NOT also grant. It answers 410 for a dead token (unknown,
// superseded, already used, expired, revoked) with nothing distinguishing
// which, deliberately.
func (t *TenancyAPI) AcceptInvitation(ctx context.Context, token, wallet, email string) (*models.RemoteInvitation, error) {
	if token == "" || wallet == "" {
		return nil, fmt.Errorf("token and wallet are required")
	}
	body := map[string]string{"token": token, "wallet": wallet}
	if email != "" {
		body["email"] = email
	}
	var res models.RemoteInvitation
	if err := t.do(ctx, asSelf(""), http.MethodPost, "/v1/invitations/accept", body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// AuthzFresh bypasses the cache. Reconciliation wants the service's current
// answer, not one this process happened to read a minute ago.
func (t *TenancyAPI) AuthzFresh(ctx context.Context, tenant models.Tenant, wallet string) (*AuthzResult, error) {
	t.authzCache.Delete(authzCacheKey(tenant.ID, wallet))
	return t.Authz(ctx, tenant, wallet)
}

// authzTTL converts the service's advertised lifetime into a duration, falling
// back when it is absent or nonsensical. A zero or negative value from the wire
// must not become "cache forever".
func authzTTL(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultAuthzTTL
	}
	return time.Duration(seconds) * time.Second
}

// get performs an authenticated GET and decodes the JSON body into out.
func (t *TenancyAPI) get(ctx context.Context, tenant models.Tenant, path string, out any) error {
	return t.do(ctx, tenant, http.MethodGet, path, nil, out)
}

// do performs one authenticated request. payload, when non-nil, is sent as
// JSON; out, when non-nil, receives the decoded response body. Both
// credentials go on every call — reads and writes share this on purpose, so
// two copies can never disagree about one of them.
func (t *TenancyAPI) do(ctx context.Context, tenant models.Tenant, method, path string, payload, out any) error {
	if !t.Configured() {
		return fmt.Errorf("%w: url=%q key set=%t", ErrTenancyNotConfigured, t.baseURL, t.apiKey != "")
	}

	// The developer JWT is minted from the tenant's own license when we hold
	// it, and from this app's identity otherwise — see authTenant. Either way
	// it is cached per identity by the auth provider, not an exchange per call.
	authAs, err := t.authTenant(tenant)
	if err != nil {
		return fmt.Errorf("tenant %s: %w", tenant.ID, err)
	}
	developerJWT, err := t.authProvider.GetDeveloperJWT(authAs)
	if err != nil {
		return fmt.Errorf("developer JWT for %s: %w", authAs.ID, err)
	}

	var reader io.Reader
	if payload != nil {
		body, merr := json.Marshal(payload)
		if merr != nil {
			return fmt.Errorf("encode tenancy request: %w", merr)
		}
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build tenancy request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(TenancyKeyHeader, t.apiKey)
	req.Header.Set("Authorization", "Bearer "+developerJWT)

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("tenancy request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read tenancy response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newTenancyError(resp.StatusCode, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("parse tenancy response: %w", err)
		}
	}
	return nil
}

// TenancyKeyHeader carries the pre-shared key. Mirrors app.TrustedCallerHeader
// in fleet-tenancy-api; deliberately not Authorization, which already carries
// the developer-license JWT.
const TenancyKeyHeader = "X-Tenancy-Key" //nolint:gosec // header name, not a credential

// newTenancyError classifies a non-2xx response by layer.
//
// The service answers errors as {"code":…,"message":…} and its messages are
// stable strings from its own handlers. Classification degrades to the status
// code alone if a body is missing or a message is reworded — the layer is a
// diagnostic, so a wrong guess must never be worse than no guess.
func newTenancyError(status int, body []byte) *TenancyError {
	var parsed struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &parsed)

	msg := parsed.Message
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}

	e := &TenancyError{StatusCode: status, Message: msg, Layer: LayerNone}
	switch status {
	case http.StatusUnauthorized:
		// Layers 1 and 2 both answer 401; only the message separates them.
		// The guard names the header in both of its rejections ("missing
		// X-Tenancy-Key", "invalid X-Tenancy-Key"), and nothing downstream
		// mentions it, so that is the discriminator.
		if strings.Contains(msg, TenancyKeyHeader) {
			e.Layer = LayerTrustedCallerKey
		} else {
			e.Layer = LayerDeveloperJWT
		}
	case http.StatusForbidden:
		e.Layer = LayerCallerScope
	}
	return e
}
