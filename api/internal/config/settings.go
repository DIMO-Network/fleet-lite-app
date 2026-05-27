package config

import (
	"net/url"

	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/ethereum/go-ethereum/common"
)

// Settings contains the application config.
type Settings struct {
	Environment    string      `yaml:"ENVIRONMENT"`
	LogLevel       string      `yaml:"LOG_LEVEL"`
	UseDevCerts    bool        `yaml:"USE_DEV_CERTS"`
	APIPort        int         `yaml:"API_PORT"`
	MonitoringPort int         `yaml:"MONITORING_PORT"`
	ServiceName    string      `yaml:"SERVICE_NAME"`
	DB             db.Settings `yaml:"DB"` // secret

	// Authentication
	JwtKeySetURL     url.URL `yaml:"JWT_KEY_SET_URL"`
	TokenExchangeURL url.URL `yaml:"TOKEN_EXCHANGE_URL"`

	// Login With DIMO
	DimoLoginURL        url.URL        `yaml:"DIMO_LOGIN_URL"`
	DimoAuthURL         url.URL        `yaml:"DIMO_AUTH_URL"`
	DimoAuthClientID    common.Address `yaml:"DIMO_AUTH_CLIENT_ID"`
	DimoAuthRedirectURL url.URL        `yaml:"DIMO_AUTH_REDIRECT_URL"`
	DimoAuthPrivateKey  string         `yaml:"DIMO_AUTH_PRIVATE_KEY"` // secret

	// CORS
	AllowedOrigins string `yaml:"ALLOWED_ORIGINS"`

	// DIMO Identity API
	IdentityAPIEndpoint url.URL `yaml:"IDENTITY_API_ENDPOINT"`

	// Chain
	ChainID           int64          `yaml:"CHAIN_ID"`
	VehicleNftAddress common.Address `yaml:"VEHICLE_NFT_ADDRESS"`
}

func (s *Settings) IsProduction() bool {
	return s.Environment == "prod"
}

func (s *Settings) IsLocal() bool {
	return s.Environment == "local"
}
