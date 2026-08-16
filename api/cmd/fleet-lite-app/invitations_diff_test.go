package main

import (
	"testing"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var diffExpiry = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func localInv() *dbmodels.Invitation {
	return &dbmodels.Invitation{
		ID:              "inv-1",
		TenantID:        "tenant-1",
		Email:           "someone@example.com",
		Role:            "member",
		Status:          "pending",
		InvitedByWallet: "0xabc",
		ExpiresAt:       diffExpiry,
	}
}

func remoteInv() models.RemoteInvitation {
	return models.RemoteInvitation{
		ID:        "inv-1",
		TenantID:  "tenant-1",
		Email:     "someone@example.com",
		Role:      "member",
		Status:    "pending",
		ExpiresAt: diffExpiry.Format(time.RFC3339),
	}
}

func verdictOf(t *testing.T, l *dbmodels.Invitation, r models.RemoteInvitation) inviteFinding {
	t.Helper()
	out := compareInvitationSets(dbmodels.InvitationSlice{l}, []models.RemoteInvitation{r})
	require.Len(t, out, 1)
	return out[0]
}

func TestCompareInvitationsAgreement(t *testing.T) {
	t.Run("identical records agree", func(t *testing.T) {
		assert.Equal(t, inviteAgree, verdictOf(t, localInv(), remoteInv()).Verdict)
	})

	// The backfill checksums wallets to EIP-55 while this app lowercases them,
	// and the service lowercases emails on create. Comparing either literally
	// would report every single row as a disagreement and make the gate
	// useless — the failure mode where a diff is so noisy nobody reads it.
	t.Run("wallet and email casing is not a disagreement", func(t *testing.T) {
		l := localInv()
		l.Email = "Someone@Example.COM"
		l.Status = "accepted"
		l.InviteeWallet = null.StringFrom("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

		r := remoteInv()
		r.Status = "accepted"
		invitee := "0xDeadBeefDeadBeefDeadBeefdeadbeefDEADBeEF"
		r.InviteeWallet = &invitee

		assert.Equal(t, inviteAgree, verdictOf(t, l, r).Verdict)
	})

	// The column holds microseconds, the wire format holds seconds.
	t.Run("sub-second expiry precision is not a disagreement", func(t *testing.T) {
		l := localInv()
		l.ExpiresAt = diffExpiry.Add(400 * time.Millisecond)
		assert.Equal(t, inviteAgree, verdictOf(t, l, remoteInv()).Verdict)
	})
}

func TestCompareInvitationsDisagreement(t *testing.T) {
	cases := map[string]func(*dbmodels.Invitation, *models.RemoteInvitation){
		"status": func(l *dbmodels.Invitation, _ *models.RemoteInvitation) { l.Status = "revoked" },
		"email":  func(l *dbmodels.Invitation, _ *models.RemoteInvitation) { l.Email = "other@example.com" },
		"role":   func(l *dbmodels.Invitation, _ *models.RemoteInvitation) { l.Role = "owner" },
		"expiry": func(l *dbmodels.Invitation, _ *models.RemoteInvitation) {
			l.ExpiresAt = diffExpiry.Add(48 * time.Hour)
		},
		"invitee wallet": func(l *dbmodels.Invitation, _ *models.RemoteInvitation) {
			l.InviteeWallet = null.StringFrom("0xaaa")
		},
	}
	for name, mutate := range cases {
		t.Run(name+" disagreeing is a failure", func(t *testing.T) {
			l, r := localInv(), remoteInv()
			mutate(l, &r)
			got := verdictOf(t, l, r)
			assert.Equal(t, inviteDiffer, got.Verdict)
			assert.NotEmpty(t, got.Detail, "a differ must say which field and both values")
		})
	}
}

// nil is "sees everything" and empty is "sees nothing". A diff that smoothed
// these together would pass an invitation whose acceptance grants the entire
// fleet to someone entitled to none of it — the exact inversion that hit 131
// memberships during the tenant backfill.
func TestCompareInvitationsScopeTriState(t *testing.T) {
	t.Run("nil local against empty remote is a disagreement", func(t *testing.T) {
		l := localInv()
		l.AllowedGroupIds = nil
		r := remoteInv()
		r.ScopeGroupIDs = []string{}

		got := verdictOf(t, l, r)
		assert.Equal(t, inviteDiffer, got.Verdict)
		assert.Contains(t, got.Detail, "nil is unrestricted")
	})

	t.Run("empty local against nil remote is a disagreement", func(t *testing.T) {
		l := localInv()
		l.AllowedGroupIds = types.StringArray{}
		r := remoteInv()
		r.ScopeGroupIDs = nil
		assert.Equal(t, inviteDiffer, verdictOf(t, l, r).Verdict)
	})

	t.Run("both unrestricted agree", func(t *testing.T) {
		assert.Equal(t, inviteAgree, verdictOf(t, localInv(), remoteInv()).Verdict)
	})

	t.Run("same groups in a different order agree", func(t *testing.T) {
		l := localInv()
		l.AllowedGroupIds = types.StringArray{"t_north", "t_vans"}
		r := remoteInv()
		r.ScopeGroupIDs = []string{"t_vans", "t_north"}
		assert.Equal(t, inviteAgree, verdictOf(t, l, r).Verdict)
	})

	t.Run("different groups disagree", func(t *testing.T) {
		l := localInv()
		l.AllowedGroupIds = types.StringArray{"t_vans"}
		r := remoteInv()
		r.ScopeGroupIDs = []string{"t_north"}
		assert.Equal(t, inviteDiffer, verdictOf(t, l, r).Verdict)
	})
}

// The asymmetry is the point: tenancy legitimately holds invitations we do not
// (the console's), but an invitation we hold that it lacks means the backfill
// has drifted and the flag must not be trusted.
func TestCompareInvitationsAsymmetry(t *testing.T) {
	t.Run("local-only is a failure", func(t *testing.T) {
		out := compareInvitationSets(dbmodels.InvitationSlice{localInv()}, nil)
		require.Len(t, out, 1)
		assert.Equal(t, inviteMissingRemote, out[0].Verdict)
	})

	t.Run("remote-only is informational and names the operator", func(t *testing.T) {
		r := remoteInv()
		r.ID = "inv-console"
		operator := "operator-tenant-uuid"
		r.CreatedByTenantID = &operator

		out := compareInvitationSets(nil, []models.RemoteInvitation{r})
		require.Len(t, out, 1)
		assert.Equal(t, inviteRemoteExtra, out[0].Verdict)
		assert.Contains(t, out[0].Detail, operator,
			"an operator-sent invite should say so — that is why the column exists")
	})

	t.Run("findings are ordered and complete", func(t *testing.T) {
		a, b := localInv(), localInv()
		b.ID = "inv-0"
		out := compareInvitationSets(dbmodels.InvitationSlice{a, b},
			[]models.RemoteInvitation{remoteInv()})
		require.Len(t, out, 2)
		assert.Equal(t, "inv-0", out[0].InvitationID, "local findings sort by id")
		assert.Equal(t, inviteMissingRemote, out[0].Verdict)
		assert.Equal(t, inviteAgree, out[1].Verdict)
	})
}
