package gateway

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/shared/pkg/dimoauth"
	"github.com/ethereum/go-ethereum/common"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog"
)

// cacheTTLFromJWT derives a cache TTL from the token's own exp claim (minus a
// safety margin) so we neither serve an expired token (fixed TTL too long) nor
// re-exchange a token that is still valid (fixed TTL too short). The token is
// parsed unverified — it comes straight from the trusted exchange and is only
// inspected for its lifetime, never trusted for auth. Falls back when exp is
// absent/unreadable or already inside the margin.
func cacheTTLFromJWT(raw string, margin, fallback time.Duration) time.Duration {
	token, _, err := jwtlib.NewParser().ParseUnverified(raw, jwtlib.MapClaims{})
	if err != nil {
		return fallback
	}
	exp, err := token.Claims.GetExpirationTime()
	if err != nil || exp == nil {
		return fallback
	}
	ttl := time.Until(exp.Time) - margin
	if ttl <= 0 {
		return fallback
	}
	return ttl
}

// DimoAuthProvider manages per-tenant DIMO developer/vehicle/asset JWTs.
//
// Two kinds of tenant, one interface. A self-serve tenant supplies its own
// developer license (client ID + decrypted private key) and mints locally. An
// operator-managed tenant has no credentials here at all — its developer JWT
// is minted by fleet-tenancy-api under the operator's license and fetched via
// the remote minter. Every DIMO call site funnels through GetDeveloperJWT /
// GetVehicleJWT / GetAssetJWT, so this split is the single place the
// difference exists; telemetry, attest, fetch and extract inherit it.
type DimoAuthProvider struct {
	mu                sync.RWMutex
	authServices      map[string]*dimoauth.AuthService // tenant ID -> auth service
	developerJWTCache *cache.Cache                     // tenant ID -> JWT
	vehicleJWTCache   *cache.Cache                     // "tenantID:tokenID|DID" -> JWT

	settings *config.Settings
	logger   zerolog.Logger

	// remoteMinter fetches a developer JWT for a tenant whose credentials this
	// app does not hold. Set after construction (UseRemoteMinter) because the
	// tenancy client and this provider need each other: tenancy authenticates
	// its calls with developer JWTs from here, and here fetches managed-tenant
	// JWTs from tenancy. The cycle is broken by identity: tenancy only ever
	// asks this provider to mint for credentialed tenants (its own identity
	// included), so a remote mint can never recurse into another remote mint.
	remoteMinter remoteDeveloperJWTMinter
}

// remoteDeveloperJWTMinter is the slice of TenancyAPI this provider needs.
type remoteDeveloperJWTMinter interface {
	DimoToken(ctx context.Context, tenantID string) (*models.RemoteMintedToken, error)
}

func NewDimoAuthProvider(logger zerolog.Logger, settings *config.Settings) *DimoAuthProvider {
	return &DimoAuthProvider{
		authServices:      make(map[string]*dimoauth.AuthService),
		developerJWTCache: cache.New(14*24*time.Hour, 15*24*time.Hour),
		vehicleJWTCache:   cache.New(10*time.Minute, 12*time.Minute),
		settings:          settings,
		logger:            logger,
	}
}

// UseRemoteMinter wires the tenancy minter for tenants whose credentials this
// app does not hold. Wired in app.go after both sides exist; see the field
// comment for why this is a setter rather than a constructor argument.
func (p *DimoAuthProvider) UseRemoteMinter(m remoteDeveloperJWTMinter) { p.remoteMinter = m }

// GetDeveloperJWT returns a cached or freshly-obtained developer JWT for the
// tenant — minted locally from its own license, or fetched from the tenancy
// minter when it has none.
func (p *DimoAuthProvider) GetDeveloperJWT(tenant models.Tenant) (string, error) {
	if cached, found := p.developerJWTCache.Get(tenant.ID); found {
		return cached.(string), nil
	}

	if tenant.ClientID == "" {
		return p.remoteDeveloperJWT(tenant)
	}

	auth, err := p.getOrCreateAuthService(tenant)
	if err != nil {
		return "", err
	}

	// Retried: the login challenge is unreliable and single-use, so a second
	// call is a fresh attempt rather than the same request repeated. See
	// mintWithRetry for the evidence that the keys are not the problem.
	jwt := mintWithRetry(auth, func(attempt int) {
		p.logger.Warn().Int("attempt", attempt).Str("tenant_id", tenant.ID).
			Msg("developer JWT mint failed, retrying with a fresh challenge")
	})
	if jwt == nil {
		return "", fmt.Errorf("failed to get developer JWT for tenant %s after %d attempts",
			tenant.ID, mintAttempts)
	}
	p.developerJWTCache.Set(tenant.ID, jwt.Raw,
		cacheTTLFromJWT(jwt.Raw, 5*time.Minute, 14*24*time.Hour))
	return jwt.Raw, nil
}

// remoteDeveloperJWT fetches a managed tenant's developer JWT — the operator's
// license, minted by fleet-tenancy-api — and caches it under the tenant id
// with the same expiry-derived TTL as local mints.
func (p *DimoAuthProvider) remoteDeveloperJWT(tenant models.Tenant) (string, error) {
	if p.remoteMinter == nil {
		return "", fmt.Errorf("tenant %s holds no credentials and no tenancy minter is wired", tenant.ID)
	}
	minted, err := p.remoteMinter.DimoToken(context.Background(), tenant.ID)
	if err != nil {
		return "", fmt.Errorf("remote developer JWT for tenant %s: %w", tenant.ID, err)
	}
	if minted.Token == "" {
		return "", fmt.Errorf("remote developer JWT for tenant %s: empty token", tenant.ID)
	}
	p.developerJWTCache.Set(tenant.ID, minted.Token,
		cacheTTLFromJWT(minted.Token, 5*time.Minute, 10*time.Minute))
	return minted.Token, nil
}

// GetVehicleJWT exchanges the tenant's developer JWT for a vehicle-scoped JWT (by tokenID).
func (p *DimoAuthProvider) GetVehicleJWT(tenant models.Tenant, tokenID uint64) (string, error) {
	cacheKey := fmt.Sprintf("%s:%d", tenant.ID, tokenID)
	if cached, found := p.vehicleJWTCache.Get(cacheKey); found {
		return cached.(string), nil
	}

	developerJWT, err := p.GetDeveloperJWT(tenant)
	if err != nil {
		return "", fmt.Errorf("dev JWT for vehicle exchange: %w", err)
	}
	auth, err := p.exchangeServiceFor(tenant)
	if err != nil {
		return "", err
	}

	vehicleJWT, err := auth.GetVehicleJWT(developerJWT, []int{1, 3, 4, 5}, tokenID)
	if err != nil {
		return "", fmt.Errorf("exchange for vehicle JWT: %w", err)
	}
	p.vehicleJWTCache.Set(cacheKey, vehicleJWT,
		cacheTTLFromJWT(vehicleJWT, time.Minute, 10*time.Minute))
	return vehicleJWT, nil
}

// assetJWTPrivileges is what a CloudEvent read needs — and nothing more.
//
// Asset JWTs are used for exactly one thing here: fetch-api reads (telemetry
// goes through GetVehicleJWT with its own set). Token-exchange refuses the
// whole request if the SACD is missing ANY privilege asked for, so every extra
// entry narrows the set of grants that can read documents at all.
//
// This is dimo-app-backend's FETCH_API_PERMISSIONS exactly. We previously also
// asked for GetLocationHistory, which no CloudEvent read uses: a share granting
// raw data but not all-time location produced a 403 here while the same
// documents read fine in the mobile app.
var assetJWTPrivileges = []string{
	"privilege:GetNonLocationHistory",
	"privilege:GetCurrentLocation",
	"privilege:GetVINCredential",
	"privilege:GetRawData",
}

// GetAssetJWT exchanges the tenant's developer JWT for an asset (DID-scoped) JWT.
// DID format: did:erc721:<chainId>:<contractAddr>:<tokenId>
func (p *DimoAuthProvider) GetAssetJWT(tenant models.Tenant, tokenDID string) (string, error) {
	cacheKey := fmt.Sprintf("%s:%s", tenant.ID, tokenDID)
	if cached, found := p.vehicleJWTCache.Get(cacheKey); found {
		return cached.(string), nil
	}

	developerJWT, err := p.GetDeveloperJWT(tenant)
	if err != nil {
		return "", fmt.Errorf("dev JWT for asset exchange: %w", err)
	}
	auth, err := p.exchangeServiceFor(tenant)
	if err != nil {
		return "", err
	}

	assetJWT, err := auth.GetAssetJWT(developerJWT, assetJWTPrivileges, tokenDID)
	if err != nil {
		return "", fmt.Errorf("exchange for asset JWT: %w", err)
	}
	p.vehicleJWTCache.Set(cacheKey, assetJWT,
		cacheTTLFromJWT(assetJWT, time.Minute, 10*time.Minute))
	return assetJWT, nil
}

// BuildVehicleDID returns the canonical asset DID for a vehicle tokenID.
func (p *DimoAuthProvider) BuildVehicleDID(tokenID uint64) string {
	return fmt.Sprintf("did:erc721:%d:%s:%d", p.settings.ChainID, p.settings.VehicleNftAddress.Hex(), tokenID)
}

// EffectiveClientID returns the dev-license client id this tenant's
// attestations are actually signed with — its own when it holds credentials,
// the operator's when fleet-tenancy-api mints for it (`tenant.ClientID == ""`
// is the sentinel for that, per GetDeveloperJWT).
//
// It matters because a CloudEvent's `source` is that client id, and fetch-api
// only suppresses a tombstone against a row with a matching `source`. Comparing
// a document's source to `tenant.ClientID` directly would mark every document
// of every managed tenant as somebody else's.
//
// Cached per tenant: the value is stable, and this is on the document-list
// path. An unresolvable id returns "" and callers must treat that as "we
// cannot claim this document", never as a match.
func (p *DimoAuthProvider) EffectiveClientID(tenant models.Tenant) string {
	if tenant.ClientID != "" {
		return tenant.ClientID
	}
	cacheKey := "client-id:" + tenant.ID
	if cached, found := p.developerJWTCache.Get(cacheKey); found {
		return cached.(string)
	}
	if p.remoteMinter == nil {
		return ""
	}
	minted, err := p.remoteMinter.DimoToken(context.Background(), tenant.ID)
	if err != nil || minted == nil || minted.ClientID == "" {
		p.logger.Warn().Err(err).Str("tenant_id", tenant.ID).
			Msg("could not resolve the tenant's effective client id; documents will read as third-party")
		return ""
	}
	p.developerJWTCache.Set(cacheKey, minted.ClientID, 10*time.Minute)
	return minted.ClientID
}

// BuildAccountDID returns the canonical account DID for a wallet address:
// `did:ethr:<chainId>:<EIP-55 address>`. This is the exact form
// dimo-app-backend's canonicalAccountDid produces, and the only form DIS and
// fetch-api accept — a 3-part did:ethr or a lowercased address is rejected or,
// worse, silently indexed apart.
//
// Used to stamp `producer` on document CEs so an uploader is attributable
// across both apps.
func (p *DimoAuthProvider) BuildAccountDID(address common.Address) string {
	return fmt.Sprintf("did:ethr:%d:%s", p.settings.ChainID, address.Hex())
}

// BuildTenantDID returns the ethr DID for a tenant's dev-license client id. It
// is the subject of tenant-level (client-id-scoped) attestations such as the
// geofence catalog. Verified against the live Attest/Fetch APIs: a bare 0x
// address is rejected ("invalid DID"), but the ethr DID form is accepted and is
// queryable via an asset JWT minted for this same DID (the dev license can
// self-grant). See docs/GEOFENCES_PLAN.md.
func (p *DimoAuthProvider) BuildTenantDID(clientID string) string {
	return fmt.Sprintf("did:ethr:%d:%s", p.settings.ChainID, clientID)
}

// ParseTokenIDFromDID extracts the numeric token ID from a DID string.
func ParseTokenIDFromDID(tokenDID string) (uint64, error) {
	parts := strings.Split(tokenDID, ":")
	if len(parts) == 0 {
		return 0, fmt.Errorf("invalid tokenDID: %s", tokenDID)
	}
	id, err := strconv.ParseUint(parts[len(parts)-1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse tokenID from DID %s: %w", tokenDID, err)
	}
	return id, nil
}

// exchangeServiceFor returns the AuthService a token exchange runs through.
//
// The exchange itself needs only the token-exchange URL — dimoauth's
// GetVehicleJWT/GetAssetJWT take the developer JWT as a parameter and never
// touch the private key — but the type is only constructible with a valid key.
// A credentialed tenant uses its own service, as before; a managed tenant,
// whose developer JWT came from the tenancy minter, exchanges through a
// service built from this app's own settings. Which key that service holds is
// irrelevant to the exchange: the JWT being exchanged is what the token
// exchange authorizes, and it is the operator's.
func (p *DimoAuthProvider) exchangeServiceFor(tenant models.Tenant) (*dimoauth.AuthService, error) {
	if tenant.ClientID != "" {
		return p.getOrCreateAuthService(tenant)
	}
	if p.settings.DimoAuthPrivateKey == "" {
		return nil, fmt.Errorf("tenant %s holds no credentials and DIMO_AUTH_PRIVATE_KEY is unset", tenant.ID)
	}
	return p.getOrCreateAuthService(models.Tenant{
		ID:             "app:exchange",
		ClientID:       p.settings.DimoAuthClientID.Hex(),
		DIMOPrivateKey: p.settings.DimoAuthPrivateKey,
	})
}

func (p *DimoAuthProvider) getOrCreateAuthService(tenant models.Tenant) (*dimoauth.AuthService, error) {
	p.mu.RLock()
	auth, found := p.authServices[tenant.ID]
	p.mu.RUnlock()
	if found {
		return auth, nil
	}

	// Prefer the tenant's registered redirect URI; fall back to the global one.
	redirect := p.settings.DimoAuthRedirectURL
	if tenant.DIMORedirectURI != "" {
		if ruri, err := url.Parse(tenant.DIMORedirectURI); err == nil {
			redirect = *ruri
		}
	}

	auth, err := dimoauth.NewAuthService(p.logger, &dimoauth.Settings{
		AuthURL:            p.settings.DimoAuthURL,
		TokenExchangeURL:   p.settings.TokenExchangeURL,
		NFTContractAddress: p.settings.VehicleNftAddress,
		ClientID:           common.HexToAddress(tenant.ClientID),
		RedirectURL:        redirect,
		PrivateKeyHex:      tenant.DIMOPrivateKey,
	})
	if err != nil {
		return nil, fmt.Errorf("create auth service for tenant %s: %w", tenant.ID, err)
	}

	p.mu.Lock()
	p.authServices[tenant.ID] = auth
	p.mu.Unlock()
	return auth, nil
}
