package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeJWTProvider struct {
	jwt string
	err error
}

func (f fakeJWTProvider) GetDeveloperJWT(models.Tenant) (string, error) { return f.jwt, f.err }

// newTestTenancyAPI points a client at srv with a fixed key and a canned JWT.
func newTestTenancyAPI(t *testing.T, srv *httptest.Server, key string) *TenancyAPI {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	api := NewTenancyAPI(zerolog.Nop(), &config.Settings{
		TenancyAPIURL: *u,
		TenancyAPIKey: key,
	}, nil)
	api.authProvider = fakeJWTProvider{jwt: "dev.jwt.token"}
	return api
}

var testTenant = models.Tenant{ID: "7be1ab9e-0000-0000-0000-000000000001", ClientID: "0xabc"}

// The two headers are the whole point of this client: one says which
// application is calling, the other which tenant it acts as. Sending only one
// is a 401 that looks exactly like sending neither.
func TestTenancyAPI_SendsBothAuthHeaders(t *testing.T) {
	var gotKey, gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get(TenancyKeyHeader)
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		assert.Equal(t, "/v1/authz", r.URL.Path)
		_ = json.NewEncoder(w).Encode(AuthzResult{TenantID: testTenant.ID, Via: "direct", Member: true})
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "psk-123")
	res, err := api.Authz(context.Background(), testTenant, "0xCAA591fA19a86762D1ed1B98b2057Ee233240b65")
	require.NoError(t, err)

	assert.Equal(t, "psk-123", gotKey)
	assert.Equal(t, "Bearer dev.jwt.token", gotAuth)
	assert.Contains(t, gotQuery, "tenant_id="+testTenant.ID)
	assert.Contains(t, gotQuery, "wallet=0xCAA591fA19a86762D1ed1B98b2057Ee233240b65")
	assert.True(t, res.Member)
}

// nil scope means unrestricted, [] means restricted to nothing. Decoding must
// preserve the difference — collapsing them grants a member who should see no
// groups the entire fleet.
func TestTenancyAPI_ScopeGroupIDsNilVersusEmpty(t *testing.T) {
	for _, tc := range []struct {
		name           string
		body           string
		wantUnrestrict bool
		wantLen        int
	}{
		{"null is unrestricted", `{"scopeGroupIds":null,"via":"direct"}`, true, 0},
		{"empty array sees nothing", `{"scopeGroupIds":[],"via":"direct"}`, false, 0},
		{"populated restricts", `{"scopeGroupIds":["t_a","t_b"],"via":"direct"}`, false, 2},
		{"absent is unrestricted", `{"via":"direct"}`, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			res, err := newTestTenancyAPI(t, srv, "k").Authz(context.Background(), testTenant, "0xabc")
			require.NoError(t, err)
			assert.Equal(t, tc.wantUnrestrict, res.Unrestricted())
			assert.Len(t, res.ScopeGroupIDs, tc.wantLen)
		})
	}
}

// Layers 1 and 2 both answer 401. Classification is what makes a failed caller
// diagnosable from the caller's side instead of by reading the service's logs
// for a warning that isn't there.
func TestTenancyAPI_ClassifiesRejectionByLayer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantLayer AccessLayer
	}{
		{"missing key", http.StatusUnauthorized, `{"code":401,"message":"missing X-Tenancy-Key"}`, LayerTrustedCallerKey},
		{"invalid key", http.StatusUnauthorized, `{"code":401,"message":"invalid X-Tenancy-Key"}`, LayerTrustedCallerKey},
		{"bad jwt", http.StatusUnauthorized, `{"code":401,"message":"invalid or missing JWT"}`, LayerDeveloperJWT},
		{"unregistered license", http.StatusUnauthorized, `{"code":401,"message":"no tenant registered for this developer license"}`, LayerDeveloperJWT},
		{"out of scope", http.StatusForbidden, `{"code":403,"message":"caller may not query this tenant"}`, LayerCallerScope},
		{"server error is not a layer", http.StatusInternalServerError, `{"code":500,"message":"authorization lookup failed"}`, LayerNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := newTestTenancyAPI(t, srv, "k").Authz(context.Background(), testTenant, "0xabc")
			require.Error(t, err)

			var tErr *TenancyError
			require.True(t, errors.As(err, &tErr), "expected a *TenancyError, got %T", err)
			assert.Equal(t, tc.wantLayer, tErr.Layer)
			assert.Equal(t, tc.status, tErr.StatusCode)
			assert.Contains(t, tErr.Error(), tErr.Message)
		})
	}
}

// A 401 whose body is not the service's JSON must still be a 401 with no layer
// claimed, rather than a confident wrong answer.
func TestTenancyAPI_UnparseableErrorBodyKeepsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("upstream proxy says no"))
	}))
	defer srv.Close()

	_, err := newTestTenancyAPI(t, srv, "k").Authz(context.Background(), testTenant, "0xabc")
	var tErr *TenancyError
	require.True(t, errors.As(err, &tErr))
	assert.Equal(t, http.StatusUnauthorized, tErr.StatusCode)
	assert.Equal(t, "upstream proxy says no", tErr.Message)
	// No X-Tenancy-Key mention, so it falls to the JWT layer rather than
	// asserting the key was fine.
	assert.Equal(t, LayerDeveloperJWT, tErr.Layer)
}

// "No access" is a 200 with via=none, not an error. The caller decides what
// status its own surface returns.
func TestTenancyAPI_NoAccessIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"via":"none","member":false,"permissions":[]}`))
	}))
	defer srv.Close()

	res, err := newTestTenancyAPI(t, srv, "k").Authz(context.Background(), testTenant, "0xabc")
	require.NoError(t, err)
	assert.Equal(t, "none", res.Via)
	assert.False(t, res.Member)
	assert.False(t, res.HasCapability("manage_members"))
}

// An unset key must name itself as the problem. Left to the wire it would come
// back as a 401 indistinguishable from a wrong key.
func TestTenancyAPI_NotConfigured(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings config.Settings
	}{
		{"no key", config.Settings{TenancyAPIURL: url.URL{Scheme: "http", Host: "tenancy:8084"}}},
		{"no url", config.Settings{TenancyAPIKey: "psk"}},
		{"neither", config.Settings{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := NewTenancyAPI(zerolog.Nop(), &tc.settings, nil)
			api.authProvider = fakeJWTProvider{jwt: "x"}
			assert.False(t, api.Configured())

			_, err := api.Authz(context.Background(), testTenant, "0xabc")
			assert.ErrorIs(t, err, ErrTenancyNotConfigured)
		})
	}
}

// A tenant whose credentials won't mint a JWT fails before the request, and the
// error says so rather than surfacing as an opaque rejection from the service.
func TestTenancyAPI_DeveloperJWTFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("request should not have been sent without a JWT")
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "k")
	api.authProvider = fakeJWTProvider{err: errors.New("Unregistered redirect_uri")}

	_, err := api.Authz(context.Background(), testTenant, "0xabc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "developer JWT")
	assert.Contains(t, err.Error(), "Unregistered redirect_uri")
}

func TestTenancyAPI_RequiresWallet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("request should not have been sent without a wallet")
	}))
	defer srv.Close()

	_, err := newTestTenancyAPI(t, srv, "k").Authz(context.Background(), testTenant, "")
	assert.ErrorContains(t, err, "wallet is required")
}

// A trailing slash on the configured URL must not produce //v1/authz.
func TestTenancyAPI_TrimsTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"via":"none"}`))
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL + "/")
	require.NoError(t, err)
	api := NewTenancyAPI(zerolog.Nop(), &config.Settings{TenancyAPIURL: *u, TenancyAPIKey: "k"}, nil)
	api.authProvider = fakeJWTProvider{jwt: "j"}

	_, err = api.Authz(context.Background(), testTenant, "0xabc")
	require.NoError(t, err)
	assert.Equal(t, "/v1/authz", gotPath)
}
