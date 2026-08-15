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

// The tenancy service refuses a membership write whose scopeGroupIds is
// absent — "omitted" and "unrestricted" must never be conflated. This pins the
// encoding: nil goes over the wire as an explicit null, an empty slice as [],
// and the field is never missing.
func TestTenancyAPI_PutMemberScopeEncoding(t *testing.T) {
	cases := []struct {
		name  string
		scope []string
		want  string
	}{
		{"unrestricted is an explicit null", nil, "null"},
		{"restricted to nothing is an empty array", []string{}, "[]"},
		{"restricted is the array", []string{"g1"}, `["g1"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]json.RawMessage
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPut, r.Method)
				assert.Equal(t, "/v1/tenants/t1/members/0xW", r.URL.Path)
				raw, _ := io.ReadAll(r.Body)
				require.NoError(t, json.Unmarshal(raw, &body))
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			api := newTestTenancyAPI(t, srv, "key")
			api.selfTenant = &appSelf

			err := api.PutMember(context.Background(), asSelf("t1"), "0xW", RemoteMemberWrite{
				Role:          "member",
				ScopeGroupIDs: tc.scope,
			})
			require.NoError(t, err)

			raw, present := body["scopeGroupIds"]
			require.True(t, present, "scopeGroupIds must never be omitted — the service 400s")
			assert.JSONEq(t, tc.want, string(raw))
			assert.JSONEq(t, `[]`, string(body["permissions"]), "nil permissions must be sent as []")
		})
	}
}

func TestTenancyAPI_DeleteMember(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/v1/tenants/t1/members/0xW", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	api := newTestTenancyAPI(t, srv, "key")
	api.selfTenant = &appSelf
	require.NoError(t, api.DeleteMember(context.Background(), asSelf("t1"), "0xW"))
	assert.True(t, called)
}
