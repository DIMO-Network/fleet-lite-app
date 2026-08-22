package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShareableOwners(t *testing.T) {
	t.Run("posts the owners and returns the shareable subset", func(t *testing.T) {
		var gotPath, gotBody string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			_ = json.NewEncoder(w).Encode(map[string][]string{
				"owners": {"0x1111111111111111111111111111111111111111"},
			})
		}))
		defer srv.Close()

		api := newTestTenancyAPI(t, srv, "psk")
		got, _, err := api.ShareableOwners(context.Background(), testTenant, []string{
			"0x1111111111111111111111111111111111111111",
			"0x2222222222222222222222222222222222222222",
		})
		require.NoError(t, err)

		assert.Equal(t, "/v1/tenants/"+testTenant.ID+"/shareable-owners", gotPath)
		assert.Contains(t, gotBody, "0x2222222222222222222222222222222222222222",
			"every candidate owner must be sent, not just the first")
		assert.Equal(t, []string{"0x1111111111111111111111111111111111111111"}, got)
	})

	// An empty list means there is nothing to ask about. Skipping the call
	// keeps a fleet with no synced vehicles from making a request per render.
	t.Run("an empty list makes no request", func(t *testing.T) {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			called = true
		}))
		defer srv.Close()

		api := newTestTenancyAPI(t, srv, "psk")
		got, _, err := api.ShareableOwners(context.Background(), testTenant, nil)
		require.NoError(t, err)
		assert.Nil(t, got)
		assert.False(t, called)
	})
}

func TestShareVehicle(t *testing.T) {
	t.Run("posts the grant and returns the job id", func(t *testing.T) {
		var gotPath string
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]int64{"jobId": 99})
		}))
		defer srv.Close()

		api := newTestTenancyAPI(t, srv, "psk")
		jobID, err := api.ShareVehicle(context.Background(), testTenant, 42,
			"0x2222222222222222222222222222222222222222", 365,
			"0x3333333333333333333333333333333333333333")
		require.NoError(t, err)

		assert.Equal(t, "/v1/tenants/"+testTenant.ID+"/vehicles/42/share", gotPath)
		assert.Equal(t, int64(99), jobID)
		assert.Equal(t, "0x2222222222222222222222222222222222222222", gotBody["grantee"])
		assert.EqualValues(t, 365, gotBody["durationDays"])
		assert.Equal(t, "0x3333333333333333333333333333333333333333", gotBody["wallet"],
			"the acting member must reach the tenancy service — it makes the capability check")
	})

	// A policy denial has to survive the hop with its status intact. Flattened
	// into a generic error it would reach the customer as "try again" for
	// something that will never succeed.
	t.Run("a 403 is a TenancyError carrying the status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"the vehicle's owner has not authorized this tenant"}`))
		}))
		defer srv.Close()

		api := newTestTenancyAPI(t, srv, "psk")
		_, err := api.ShareVehicle(context.Background(), testTenant, 42,
			"0x2222222222222222222222222222222222222222", 0, "0x3333333333333333333333333333333333333333")
		require.Error(t, err)

		var terr *TenancyError
		require.ErrorAs(t, err, &terr)
		assert.Equal(t, http.StatusForbidden, terr.StatusCode)
	})
}

// Success is the isSuccessful boolean, never a "Success" string. kaufmann
// carries both conventions for different operations and mixing them is a
// recorded trap, so the boolean is asserted directly.
func TestShareStatus(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(ShareJobStatus{
			JobID: 99, State: "completed", IsSuccessful: true, Errors: []string{},
		})
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "psk")
	got, err := api.ShareStatus(context.Background(), testTenant, 42, 99)
	require.NoError(t, err)

	assert.Equal(t, "/v1/tenants/"+testTenant.ID+"/vehicles/42/share/status", gotPath)
	assert.Equal(t, "jobId=99", gotQuery)
	assert.True(t, got.IsSuccessful)
	assert.Equal(t, "completed", got.State)
}
