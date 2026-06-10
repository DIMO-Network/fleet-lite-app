package gateway

import (
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

// DimoAuthProvider manages per-tenant DIMO developer/vehicle/asset JWTs. Each
// tenant supplies its own developer license (client ID + decrypted private key);
// all outbound DIMO data calls run under the current tenant's credentials.
type DimoAuthProvider struct {
	mu                sync.RWMutex
	authServices      map[string]*dimoauth.AuthService // tenant ID -> auth service
	developerJWTCache *cache.Cache                     // tenant ID -> JWT
	vehicleJWTCache   *cache.Cache                     // "tenantID:tokenID|DID" -> JWT

	settings *config.Settings
	logger   zerolog.Logger
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

// GetDeveloperJWT returns a cached or freshly-obtained developer JWT for the tenant.
func (p *DimoAuthProvider) GetDeveloperJWT(tenant models.Tenant) (string, error) {
	if cached, found := p.developerJWTCache.Get(tenant.ID); found {
		return cached.(string), nil
	}

	auth, err := p.getOrCreateAuthService(tenant)
	if err != nil {
		return "", err
	}

	jwt := auth.GetToken()
	if jwt == nil {
		return "", fmt.Errorf("failed to get developer JWT for tenant %s", tenant.ID)
	}
	p.developerJWTCache.Set(tenant.ID, jwt.Raw,
		cacheTTLFromJWT(jwt.Raw, 5*time.Minute, 14*24*time.Hour))
	return jwt.Raw, nil
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
	auth, err := p.getOrCreateAuthService(tenant)
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
	auth, err := p.getOrCreateAuthService(tenant)
	if err != nil {
		return "", err
	}

	assetJWT, err := auth.GetAssetJWT(developerJWT, []string{
		"privilege:GetNonLocationHistory",
		"privilege:GetCurrentLocation",
		"privilege:GetLocationHistory",
		"privilege:GetVINCredential",
		"privilege:GetRawData",
	}, tokenDID)
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
