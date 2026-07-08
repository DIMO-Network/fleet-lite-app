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

	// Multi-tenancy: key used to AES-256-GCM encrypt tenant DIMO API keys at rest. // secret
	TenantSecretEncKey string `yaml:"TENANT_SECRET_ENC_KEY"`

	// DIMO Identity API
	IdentityAPIEndpoint url.URL `yaml:"IDENTITY_API_ENDPOINT"`

	// DIMO Document services (extract + attest + fetch).
	// Used by the glovebox feature; see docs/GLOVEBOX.md.
	ExtractAPIURL url.URL `yaml:"EXTRACT_API_URL"`
	FetchAPIURL   url.URL `yaml:"FETCH_API_URL"`
	AttestAPIURL  url.URL `yaml:"ATTEST_API_URL"`

	// DIMO Telemetry API (vehicle-details charts).
	TelemetryAPIURL url.URL `yaml:"TELEMETRY_API_URL"`

	// Chain
	ChainID           int64          `yaml:"CHAIN_ID"`
	VehicleNftAddress common.Address `yaml:"VEHICLE_NFT_ADDRESS"`

	// Member invitations (Postmark transactional email).
	// PostmarkServerToken authenticates against the Postmark API (server-scoped).
	// InvitationFromEmail must be a verified Postmark sender signature/domain.
	// InvitationTemplateAlias is the Postmark template alias the invite email uses
	// (see api/templates/postmark, pushed via `make push-postmark-templates`).
	// AppBaseURL is the public origin used to build the accept link.
	// InviteExpiryHours bounds how long an invite token stays valid.
	// PostmarkWebhookSecret is the basic-auth password Postmark presents when
	// POSTing delivery/open/bounce events to /webhooks/postmark; empty disables
	// the endpoint. See docs/POSTMARK_WEBHOOK_PLAN.md.
	PostmarkServerToken     string  `yaml:"POSTMARK_SERVER_TOKEN"`   // secret
	PostmarkWebhookSecret   string  `yaml:"POSTMARK_WEBHOOK_SECRET"` // secret
	InvitationFromEmail     string  `yaml:"INVITATION_FROM_EMAIL"`
	InvitationTemplateAlias string  `yaml:"POSTMARK_INVITATION_TEMPLATE_ALIAS"`
	AppBaseURL              url.URL `yaml:"APP_BASE_URL"`
	InviteExpiryHours       int     `yaml:"INVITE_EXPIRY_HOURS"`
}

func (s *Settings) IsProduction() bool {
	return s.Environment == "prod"
}

func (s *Settings) IsLocal() bool {
	return s.Environment == "local"
}
