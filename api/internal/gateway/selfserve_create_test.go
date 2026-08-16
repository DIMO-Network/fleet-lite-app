package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTenancyAPI_CreateSelfServeTenant(t *testing.T) {
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/tenants", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"minted-uuid","name":"My Fleet","kind":"customer","status":"active","entitlementMode":"implicit","fleetLiteEnabled":true}`))
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "key")
	api.selfTenant = &appSelf

	created, err := api.CreateSelfServeTenant(context.Background(),
		"My Fleet", "0xLicense", "secret-key", "0xOwner", "owner@example.com")
	require.NoError(t, err)
	assert.Equal(t, "minted-uuid", created.ID, "the minted id is what the local row must reuse")
	assert.Equal(t, map[string]string{
		"name": "My Fleet", "clientId": "0xLicense", "apiKey": "secret-key",
		"ownerWallet": "0xOwner", "ownerEmail": "owner@example.com",
	}, body)
}

// An id-less answer must be an error, not a tenant the caller would then
// materialise under uuid "".
func TestTenancyAPI_CreateSelfServeTenantRefusesEmptyID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"My Fleet"}`))
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "key")
	api.selfTenant = &appSelf
	_, err := api.CreateSelfServeTenant(context.Background(), "My Fleet", "0xL", "k", "0xO", "")
	assert.Error(t, err)
}

// A 409 (license already registered) must surface with its layer and status
// intact — the controller passes it through to the person who typed the id.
func TestTenancyAPI_CreateSelfServeTenantConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":409,"message":"that developer license is already registered to a tenant"}`))
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "key")
	api.selfTenant = &appSelf
	_, err := api.CreateSelfServeTenant(context.Background(), "My Fleet", "0xL", "k", "0xO", "")
	var te *TenancyError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, http.StatusConflict, te.StatusCode)
}

func TestTenancyAPI_PutTenantCredentials(t *testing.T) {
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/v1/tenants/t1/credentials", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "key")
	api.selfTenant = &appSelf
	require.NoError(t, api.PutTenantCredentials(context.Background(), "t1", "0xNew", "new-key"))
	assert.Equal(t, map[string]string{"clientId": "0xNew", "apiKey": "new-key"}, body)
}
