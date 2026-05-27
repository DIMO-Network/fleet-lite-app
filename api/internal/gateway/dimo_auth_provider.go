package gateway

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/shared/pkg/dimoauth"
	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog"
)

// DimoAuthProvider holds a single DIMO developer license (configured in
// settings.yaml) and provides cached developer / vehicle / asset JWTs.
//
// Single-license version of the rental-fleets DimoAuthProvider — there is no
// per-tenant table here; fleet-lite-app is single-user.
type DimoAuthProvider struct {
	authService       *dimoauth.AuthService
	authServiceErr    error
	authServiceOnce   sync.Once
	developerJWTCache *cache.Cache // "_" -> JWT string
	vehicleJWTCache   *cache.Cache // tokenID or DID -> JWT string

	settings *config.Settings
	logger   zerolog.Logger
}

func NewDimoAuthProvider(logger zerolog.Logger, settings *config.Settings) *DimoAuthProvider {
	return &DimoAuthProvider{
		developerJWTCache: cache.New(14*24*time.Hour, 15*24*time.Hour),
		vehicleJWTCache:   cache.New(10*time.Minute, 12*time.Minute),
		settings:          settings,
		logger:            logger,
	}
}

// GetDeveloperJWT returns a cached or freshly-obtained developer JWT for the
// configured DIMO_AUTH_CLIENT_ID / DIMO_AUTH_PRIVATE_KEY pair.
func (p *DimoAuthProvider) GetDeveloperJWT() (string, error) {
	if cached, found := p.developerJWTCache.Get("_"); found {
		return cached.(string), nil
	}

	auth, err := p.getOrCreateAuthService()
	if err != nil {
		return "", err
	}

	jwt := auth.GetToken()
	if jwt == nil {
		return "", fmt.Errorf("failed to get developer JWT")
	}
	p.developerJWTCache.Set("_", jwt.Raw, 14*24*time.Hour)
	return jwt.Raw, nil
}

// GetVehicleJWT exchanges the developer JWT for a vehicle-scoped JWT (by
// numeric tokenID). Used for telemetry / fetch-api by tokenID.
func (p *DimoAuthProvider) GetVehicleJWT(tokenID uint64) (string, error) {
	cacheKey := strconv.FormatUint(tokenID, 10)
	if cached, found := p.vehicleJWTCache.Get(cacheKey); found {
		return cached.(string), nil
	}

	developerJWT, err := p.GetDeveloperJWT()
	if err != nil {
		return "", fmt.Errorf("dev JWT for vehicle exchange: %w", err)
	}
	auth, err := p.getOrCreateAuthService()
	if err != nil {
		return "", err
	}

	vehicleJWT, err := auth.GetVehicleJWT(developerJWT, []int{1, 3, 4, 5}, tokenID)
	if err != nil {
		return "", fmt.Errorf("exchange for vehicle JWT: %w", err)
	}
	p.vehicleJWTCache.Set(cacheKey, vehicleJWT, 10*time.Minute)
	return vehicleJWT, nil
}

// GetAssetJWT exchanges the developer JWT for an asset (DID-scoped) JWT.
// Used for fetch-api queries that take a DID. DID format:
//
//	did:erc721:<chainId>:<contractAddr>:<tokenId>
func (p *DimoAuthProvider) GetAssetJWT(tokenDID string) (string, error) {
	if cached, found := p.vehicleJWTCache.Get(tokenDID); found {
		return cached.(string), nil
	}

	developerJWT, err := p.GetDeveloperJWT()
	if err != nil {
		return "", fmt.Errorf("dev JWT for asset exchange: %w", err)
	}
	auth, err := p.getOrCreateAuthService()
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
	p.vehicleJWTCache.Set(tokenDID, assetJWT, 10*time.Minute)
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

func (p *DimoAuthProvider) getOrCreateAuthService() (*dimoauth.AuthService, error) {
	p.authServiceOnce.Do(func() {
		auth, err := dimoauth.NewAuthService(p.logger, &dimoauth.Settings{
			AuthURL:            p.settings.DimoAuthURL,
			TokenExchangeURL:   p.settings.TokenExchangeURL,
			NFTContractAddress: p.settings.VehicleNftAddress,
			ClientID:           p.settings.DimoAuthClientID,
			RedirectURL:        p.settings.DimoAuthRedirectURL,
			PrivateKeyHex:      p.settings.DimoAuthPrivateKey,
		})
		if err != nil {
			p.authServiceErr = fmt.Errorf("create auth service: %w", err)
			return
		}
		p.authService = auth
	})
	return p.authService, p.authServiceErr
}
