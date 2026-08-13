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
	return &TenancyAPI{
		logger:       logger,
		authProvider: authProvider,
		baseURL:      strings.TrimSuffix(settings.TenancyAPIURL.String(), "/"),
		apiKey:       settings.TenancyAPIKey,
		client:       &http.Client{Timeout: tenancyTimeout},
		authzCache:   cache.New(defaultAuthzTTL, 2*defaultAuthzTTL),
	}
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

	// The developer JWT is minted from the tenant's own license, so this is
	// both our proof of identity and the reason the scope check passes. It is
	// cached per tenant by the auth provider — this is not an exchange per call.
	developerJWT, err := t.authProvider.GetDeveloperJWT(tenant)
	if err != nil {
		return fmt.Errorf("developer JWT for tenant %s: %w", tenant.ID, err)
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
