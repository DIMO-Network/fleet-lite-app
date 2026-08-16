package service

import (
	"testing"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Both invitation paths — local table and fleet-tenancy-api — answer the same
// shape, so the cutover cannot change what the frontend sees. This is the
// local side of that conversion, and the fields it must not mangle are the
// ones the Members screen renders and the ones that decide access.
func TestLocalInvitationToRemote(t *testing.T) {
	created := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	expires := created.Add(7 * 24 * time.Hour)

	base := func() *dbmodels.Invitation {
		return &dbmodels.Invitation{
			ID:              "inv-1",
			TenantID:        "tenant-1",
			Email:           "someone@example.com",
			Role:            "member",
			Status:          "pending",
			InvitedByWallet: "0xabc",
			CreatedAt:       created,
			ExpiresAt:       expires,
		}
	}

	t.Run("nil in, nil out", func(t *testing.T) {
		// Create and Resend hand back a possibly-nil row alongside a
		// partial-success error; the conversion must tolerate it.
		assert.Nil(t, localInvitationToRemote(nil))
	})

	t.Run("core fields carry over with UTC RFC3339 timestamps", func(t *testing.T) {
		out := localInvitationToRemote(base())
		require.NotNil(t, out)
		assert.Equal(t, "inv-1", out.ID)
		assert.Equal(t, "tenant-1", out.TenantID)
		assert.Equal(t, "someone@example.com", out.Email)
		assert.Equal(t, "pending", out.Status)
		assert.Equal(t, "0xabc", out.InvitedBy)
		assert.Equal(t, "2026-08-01T12:00:00Z", out.CreatedAt)
		assert.Equal(t, "2026-08-08T12:00:00Z", out.ExpiresAt)
	})

	// The inversion that has bitten this programme repeatedly: nil is "sees
	// everything", empty is "sees nothing", and the invite's scope becomes the
	// membership's scope verbatim at accept.
	t.Run("the scope tri-state survives the conversion", func(t *testing.T) {
		unrestricted := localInvitationToRemote(base())
		assert.Nil(t, unrestricted.ScopeGroupIDs, "NULL allowed_group_ids means unrestricted")

		nothing := base()
		nothing.AllowedGroupIds = types.StringArray{}
		got := localInvitationToRemote(nothing)
		assert.NotNil(t, got.ScopeGroupIDs, "an empty array is a restriction, not an absence")
		assert.Empty(t, got.ScopeGroupIDs)

		scoped := base()
		scoped.AllowedGroupIds = types.StringArray{"t_vans"}
		assert.Equal(t, []string{"t_vans"}, localInvitationToRemote(scoped).ScopeGroupIDs)
	})

	// The badge contract, preserved from the controller test this replaced: a
	// valid-but-empty status must serialize as absent, or the screen renders an
	// unknown state instead of "Not sent".
	t.Run("valid-but-empty tracking fields are treated as absent", func(t *testing.T) {
		inv := base()
		inv.EmailStatus = null.StringFrom("")
		inv.EmailStatusDetail = null.StringFrom("")
		out := localInvitationToRemote(inv)
		assert.Nil(t, out.EmailStatus)
		assert.Nil(t, out.EmailStatusDetail)
	})

	t.Run("delivery tracking and acceptance carry over", func(t *testing.T) {
		at := time.Date(2026, 8, 1, 7, 30, 0, 0, time.FixedZone("UTC-5", -5*3600))
		inv := base()
		inv.Status = "accepted"
		inv.InviteeWallet = null.StringFrom("0xdef")
		inv.AcceptedAt = null.TimeFrom(created)
		inv.EmailStatus = null.StringFrom("bounced")
		inv.EmailStatusAt = null.TimeFrom(at)
		inv.EmailStatusDetail = null.StringFrom("HardBounce: address does not exist")

		out := localInvitationToRemote(inv)
		require.NotNil(t, out.InviteeWallet)
		assert.Equal(t, "0xdef", *out.InviteeWallet)
		require.NotNil(t, out.AcceptedAt)
		assert.Equal(t, "2026-08-01T12:00:00Z", *out.AcceptedAt)
		require.NotNil(t, out.EmailStatus)
		assert.Equal(t, "bounced", *out.EmailStatus)
		require.NotNil(t, out.EmailStatusAt)
		assert.Equal(t, "2026-08-01T12:30:00Z", *out.EmailStatusAt,
			"timestamps are emitted in UTC regardless of the stored zone")
		require.NotNil(t, out.EmailStatusDetail)
	})

	// A local row never carries an operator attribution — that column only
	// exists in the shared model, and only the console sets it.
	t.Run("locally-created invitations carry no issuing tenant", func(t *testing.T) {
		assert.Nil(t, localInvitationToRemote(base()).CreatedByTenantID)
	})
}

// An unconfigured or absent tenancy client keeps everything local whatever the
// flag says — that is what makes local development work with no tenancy
// service, and it is the revert path if the flag is ever flipped by mistake
// before the client is wired.
func TestInvitesFromTenancyRequiresAConfiguredClient(t *testing.T) {
	s := &InvitationService{}
	assert.False(t, s.invitesFromTenancy(), "no client, no flag")

	s.fromTenancy = true
	assert.False(t, s.invitesFromTenancy(), "flag on but no client must stay local")
}
