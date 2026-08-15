package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var appSelf = models.Tenant{
	ID:             "app:fleet-lite",
	ClientID:       "0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB",
	DIMOPrivateKey: "aa",
}

// The identity split is the core of the managed-tenant path: a tenant whose
// credentials we hold authenticates as itself, one whose credentials we do
// not authenticates as this app. Getting it backwards either breaks every
// self-serve tenant or sends the zero client id to the tenancy service.
func TestTenancyAPI_AuthTenantSelection(t *testing.T) {
	api := &TenancyAPI{selfTenant: &appSelf}

	got, err := api.authTenant(testTenant)
	require.NoError(t, err)
	assert.Equal(t, testTenant.ID, got.ID, "a credentialed tenant authenticates as itself")

	got, err = api.authTenant(models.Tenant{ID: "managed-tenant"})
	require.NoError(t, err)
	assert.Equal(t, appSelf.ID, got.ID, "a credential-less tenant authenticates as the app")

	api.selfTenant = nil
	_, err = api.authTenant(models.Tenant{ID: "managed-tenant"})
	assert.True(t, errors.Is(err, ErrAppIdentityNotConfigured), "got %v", err)
}

func TestAppSelfTenant(t *testing.T) {
	assert.Nil(t, AppSelfTenant(&config.Settings{}), "no client id and no key")
	assert.Nil(t, AppSelfTenant(&config.Settings{
		DimoAuthClientID: common.HexToAddress("0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB"),
	}), "client id without a key cannot mint")

	self := AppSelfTenant(&config.Settings{
		DimoAuthClientID:   common.HexToAddress("0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB"),
		DimoAuthPrivateKey: "aa",
	})
	require.NotNil(t, self)
	assert.Equal(t, "app:fleet-lite", self.ID)
	assert.Equal(t, "0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB", self.ClientID)
}

func TestTenancyAPI_WalletTenants(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/tenants", r.URL.Path)
		assert.Equal(t, "0xWallet", r.URL.Query().Get("wallet"))
		assert.Equal(t, "fleet_lite", r.URL.Query().Get("surface"))
		w.Header().Set("Content-Type", "application/json")
		// One unrestricted membership, one restricted to nothing — the nil/[]
		// split must survive the wire.
		_, _ = w.Write([]byte(`[
			{"tenantId":"t1","name":"TRAST","kind":"customer","entitlementMode":"explicit","role":"admin","permissions":["manage_members"],"scopeGroupIds":null},
			{"tenantId":"t2","name":"Other","kind":"customer","entitlementMode":"explicit","role":"member","permissions":[],"scopeGroupIds":[]}
		]`))
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "key")
	api.selfTenant = &appSelf

	rows, err := api.WalletTenants(context.Background(), "0xWallet")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "TRAST", rows[0].Name)
	assert.Equal(t, "admin", rows[0].Role)
	assert.Nil(t, rows[0].ScopeGroupIDs)
	assert.NotNil(t, rows[1].ScopeGroupIDs)
	assert.Empty(t, rows[1].ScopeGroupIDs)

	_, err = api.WalletTenants(context.Background(), "")
	assert.Error(t, err, "a wallet is required")
}

// Without the app identity, the wallet listing has nothing to authenticate as
// — the failure must be the named configuration error, not a mint failure.
func TestTenancyAPI_WalletTenantsNeedsAppIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("no request should be sent without an identity")
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "key") // selfTenant nil
	_, err := api.WalletTenants(context.Background(), "0xWallet")
	assert.True(t, errors.Is(err, ErrAppIdentityNotConfigured), "got %v", err)
}

func TestTenancyAPI_TenantDetailAndDimoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/tenants/t1":
			_, _ = w.Write([]byte(`{"id":"t1","name":"TRAST","kind":"customer","status":"active","entitlementMode":"explicit","fleetLiteEnabled":true,"vehicleCount":1}`))
		case "/v1/tenants/t1/dimo-token":
			_, _ = w.Write([]byte(`{"token":"op.jwt","expiresAt":"2026-08-15T20:00:00Z","clientId":"0xOperator","credentialTenantId":"op-uuid"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "key")
	api.selfTenant = &appSelf

	detail, err := api.TenantDetail(context.Background(), "t1")
	require.NoError(t, err)
	assert.Equal(t, "TRAST", detail.Name)
	assert.Equal(t, "explicit", detail.EntitlementMode)
	assert.True(t, detail.FleetLiteEnabled)

	minted, err := api.DimoToken(context.Background(), "t1")
	require.NoError(t, err)
	assert.Equal(t, "op.jwt", minted.Token)
	assert.Equal(t, "0xOperator", minted.ClientID)
	assert.False(t, minted.ExpiresAt.IsZero())
}

func TestTenancyAPI_EntitlementsAndLoginTouch(t *testing.T) {
	var loginBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tenants/t1/vehicles":
			_, _ = w.Write([]byte(`[{"vehicleTokenId":190171,"source":"operator","sourceGroupId":null}]`))
		case r.URL.Path == "/v1/tenants/t1/members/0xW/login" && r.Method == http.MethodPost:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&loginBody))
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "key")
	api.selfTenant = &appSelf
	managed := models.Tenant{ID: "t1"} // no credentials — exercises the self path end to end

	ents, err := api.Entitlements(context.Background(), managed)
	require.NoError(t, err)
	require.Len(t, ents, 1)
	assert.Equal(t, int64(190171), ents[0].VehicleTokenID)
	assert.Equal(t, "operator", ents[0].Source)

	require.NoError(t, api.LoginTouch(context.Background(), managed, "0xW", "who@example.com"))
	assert.Equal(t, map[string]string{"email": "who@example.com"}, loginBody)
}

type fakeMinter struct {
	calls int32
	token string
	err   error
}

func (f *fakeMinter) DimoToken(context.Context, string) (*models.RemoteMintedToken, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return nil, f.err
	}
	return &models.RemoteMintedToken{Token: f.token, ClientID: "0xOperator"}, nil
}

// The remote-minter path is the choke point every DIMO gateway inherits: a
// credential-less tenant's developer JWT comes from tenancy, is cached, and a
// second ask must not hit the minter again.
func TestDimoAuthProvider_RemoteMinter(t *testing.T) {
	p := NewDimoAuthProvider(zerolog.Nop(), &config.Settings{})
	managed := models.Tenant{ID: "managed-1"}

	_, err := p.GetDeveloperJWT(managed)
	assert.Error(t, err, "no minter wired must be a named failure")

	minter := &fakeMinter{token: "operator.dev.jwt"}
	p.UseRemoteMinter(minter)

	jwt, err := p.GetDeveloperJWT(managed)
	require.NoError(t, err)
	assert.Equal(t, "operator.dev.jwt", jwt)

	jwt, err = p.GetDeveloperJWT(managed)
	require.NoError(t, err)
	assert.Equal(t, "operator.dev.jwt", jwt)
	assert.Equal(t, int32(1), atomic.LoadInt32(&minter.calls), "second read must come from the cache")

	p2 := NewDimoAuthProvider(zerolog.Nop(), &config.Settings{})
	p2.UseRemoteMinter(&fakeMinter{err: errors.New("boom")})
	_, err = p2.GetDeveloperJWT(managed)
	assert.Error(t, err)

	p3 := NewDimoAuthProvider(zerolog.Nop(), &config.Settings{})
	p3.UseRemoteMinter(&fakeMinter{token: ""})
	_, err = p3.GetDeveloperJWT(managed)
	assert.Error(t, err, "an empty token must not be cached as a credential")
}

// A credentialed tenant must never take the remote path — its mint stays
// local even when a minter is wired.
func TestDimoAuthProvider_CredentialedTenantStaysLocal(t *testing.T) {
	p := NewDimoAuthProvider(zerolog.Nop(), &config.Settings{})
	minter := &fakeMinter{token: "operator.dev.jwt"}
	p.UseRemoteMinter(minter)

	// An invalid key makes the local path fail loudly — which is the proof it
	// was the local path, since the remote fake would have succeeded.
	_, err := p.GetDeveloperJWT(models.Tenant{ID: "self-serve", ClientID: "0xabc", DIMOPrivateKey: "not-hex"})
	assert.Error(t, err)
	assert.Equal(t, int32(0), atomic.LoadInt32(&minter.calls))
}
