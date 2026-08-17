package config

import (
	"fmt"
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
	//
	// MUST be set outside local — see Validate. An empty value is not "no
	// encryption", it is encryption under sha256(""), which is a valid AES-256
	// key that anybody can compute. Nothing errors and nothing logs.
	TenantSecretEncKey string `yaml:"TENANT_SECRET_ENC_KEY"`

	// fleet-tenancy-api — the shared source of truth for tenants, users,
	// memberships and entitlements. Cluster-internal only; its chart publishes
	// no ingress, so TenancyAPIURL is a .svc.cluster.local address.
	//
	// Every /v1 request carries two headers answering two different questions:
	// TenancyAPIKey as X-Tenancy-Key ("is this a trusted application?"), and a
	// per-tenant developer-license JWT as Authorization ("which tenant is it
	// acting as?"). Holding the key is not authority to act for a tenant — see
	// gateway.TenancyAPI.
	//
	// Empty values are not a boot failure here, unlike TENANT_SECRET_ENC_KEY: a
	// missing key costs a 401 on a call this app does not yet make, whereas a
	// missing encryption key silently encrypts under a public constant. The
	// client reports the misconfiguration when a call is actually attempted.
	TenancyAPIURL url.URL `yaml:"TENANCY_API_URL"`
	TenancyAPIKey string  `yaml:"TENANCY_API_KEY"` // secret

	// GroupsFromTenancy only chooses where the display READS come from —
	// fleet-tenancy-api when on, the local mirror tables when off. Writes go
	// to tenancy unconditionally since P4; it owns the record either way.
	//
	// TEMPORARY: both the flag and the local tables go away in P5, when the
	// scope-filtering SQL stops joining against the mirror.
	GroupsFromTenancy bool `yaml:"GROUPS_FROM_TENANCY"`

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

	// Member invitations are served entirely by fleet-tenancy-api since P4 of
	// its docs/plans/04-invitations-into-tenancy.md. It holds the Postmark
	// credentials, the templates, the accept-link origin and the token expiry,
	// and it receives the delivery webhooks. Nothing invitation-shaped is
	// configured here any more — if it needs to be again, it belongs there.
}

func (s *Settings) IsProduction() bool {
	return s.Environment == "prod"
}

func (s *Settings) IsLocal() bool {
	return s.Environment == "local"
}

// Validate rejects configurations that would silently do the wrong thing.
//
// The empty-encryption-key case is the reason this exists. sha256("") is a
// valid AES-256 key, so encryptSecret succeeds, the ciphertext looks fine, and
// every tenant's DIMO developer-license private key is protected by a constant
// that is public knowledge. There is no error and no log line — it can only be
// caught here. This reached production; the check is why it cannot again.
func (s *Settings) Validate() error {
	if s.TenantSecretEncKey == "" && !s.IsLocal() {
		return fmt.Errorf("TENANT_SECRET_ENC_KEY is empty in environment %q: tenant "+
			"credentials would be encrypted with sha256(\"\"), a publicly known key",
			s.Environment)
	}
	return nil
}
