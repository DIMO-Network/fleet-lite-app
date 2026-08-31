package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strptr(s string) *string { return &s }

// A credential-less implicit-mode tenant reaches SyncVehicles through the
// entitled path (no ClientID) and never touches the DB — every case below
// returns at the mode check, so these run without a store.
func implicitTenancy(parent *string) *fakeTenancySource {
	return &fakeTenancySource{
		configured: true,
		detail: &models.RemoteTenantDetail{
			ID: "t-1", Name: "DIMO Build", Kind: "operator",
			Status: "active", EntitlementMode: "implicit",
			FleetLiteEnabled: true, ParentTenantID: parent,
		},
	}
}

// The DIMO Build case: no parent, and the minter says no credential resolves.
// Nothing backs this tenant, so it must be distinguishable from a sync that
// broke — sync-vehicles skips it without failing the run.
func TestSyncVehicles_UnparentedWithNoCredentialIsUnconfigured(t *testing.T) {
	ten := implicitTenancy(nil)
	ten.tokenErr = &gateway.TenancyError{StatusCode: 409, Message: "tenant has no effective credential"}

	_, err := newSvc(ten, nil, nil).SyncVehicles(context.Background(), &models.Tenant{ID: "t-1"})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTenantUnconfigured)
	assert.Contains(t, err.Error(), "t-1", "the summary names the tenant, so the error must carry it")
}

// An unparented tenant whose minter DOES return a credential holds its own
// license. Implicit sync from it is unimplemented, not unconfigured — quietly
// skipping would hand a real fleet an empty vehicle list.
func TestSyncVehicles_UnparentedWithCredentialStillFails(t *testing.T) {
	ten := implicitTenancy(nil)
	ten.token = &models.RemoteMintedToken{Token: "jwt", ClientID: "0xLicense"}

	_, err := newSvc(ten, nil, nil).SyncVehicles(context.Background(), &models.Tenant{ID: "t-1"})

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrTenantUnconfigured)
	assert.Contains(t, err.Error(), "holds no credentials")
}

// A parented tenant resolves its operator's license, so it is reachable and
// the mode is the mismatch. No minter call is needed to know that.
func TestSyncVehicles_ParentedTenantStillFailsWithoutMinterCall(t *testing.T) {
	ten := implicitTenancy(strptr("operator-uuid"))

	_, err := newSvc(ten, nil, nil).SyncVehicles(context.Background(), &models.Tenant{ID: "t-1"})

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrTenantUnconfigured)
	assert.Zero(t, ten.tokenCalls, "a parent settles it; don't spend a mint to re-ask")
}

// An unreachable tenancy service must not be able to mark every tenant
// unconfigured and silence the alert wholesale.
func TestSyncVehicles_MinterTransientFailureIsNotUnconfigured(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"upstream", &gateway.TenancyError{StatusCode: 502, Message: "upstream failure"}},
		{"scope", &gateway.TenancyError{StatusCode: 403, Message: "caller may not act on this tenant"}},
		{"transport", errors.New("dial tcp: connection refused")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ten := implicitTenancy(nil)
			ten.tokenErr = tc.err

			_, err := newSvc(ten, nil, nil).SyncVehicles(context.Background(), &models.Tenant{ID: "t-1"})

			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrTenantUnconfigured)
		})
	}
}
