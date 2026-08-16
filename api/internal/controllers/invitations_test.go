package controllers

import (
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
)

func strp(s string) *string { return &s }

// The Members settings view renders a delivery badge straight off these fields,
// and it distinguishes "sent but unconfirmed" from "never dispatched" by the
// presence of emailStatus. So an unsent invitation must serialize with all three
// tracking fields absent (nil), never as empty strings — an empty-but-present
// status would render as an unknown state instead of "Not sent".
//
// The other half of this contract — that a local row's valid-but-empty
// null.String becomes a nil pointer — moved to the service package with the
// conversion itself; see TestLocalInvitationToRemote.
func TestToInvitationJSONEmailTracking(t *testing.T) {
	base := func() *models.RemoteInvitation {
		return &models.RemoteInvitation{
			ID:        "inv-1",
			Email:     "someone@example.com",
			Role:      "member",
			Status:    "pending",
			CreatedAt: "2026-08-01T12:00:00Z",
			ExpiresAt: "2026-08-08T12:00:00Z",
		}
	}

	t.Run("never dispatched omits all tracking fields", func(t *testing.T) {
		out := toInvitationJSON(base())
		if out.EmailStatus != nil {
			t.Errorf("EmailStatus = %v; want nil", *out.EmailStatus)
		}
		if out.EmailStatusAt != nil {
			t.Errorf("EmailStatusAt = %v; want nil", *out.EmailStatusAt)
		}
		if out.EmailStatusDetail != nil {
			t.Errorf("EmailStatusDetail = %v; want nil", *out.EmailStatusDetail)
		}
	})

	t.Run("bounced carries status, timestamp and reason", func(t *testing.T) {
		inv := base()
		inv.EmailStatus = strp("bounced")
		inv.EmailStatusAt = strp("2026-08-01T12:30:00Z")
		inv.EmailStatusDetail = strp("HardBounce: address does not exist")

		out := toInvitationJSON(inv)
		if out.EmailStatus == nil || *out.EmailStatus != "bounced" {
			t.Fatalf("EmailStatus = %v; want bounced", out.EmailStatus)
		}
		if out.EmailStatusAt == nil || *out.EmailStatusAt != "2026-08-01T12:30:00Z" {
			t.Errorf("EmailStatusAt = %v; want the stamped time", out.EmailStatusAt)
		}
		if out.EmailStatusDetail == nil || *out.EmailStatusDetail != "HardBounce: address does not exist" {
			t.Errorf("EmailStatusDetail = %v; want the bounce reason", out.EmailStatusDetail)
		}
	})
}

// The wire field is allowedGroupIds and must stay that way: the frontend reads
// it, and the records moving to another service is not a reason to rename what
// this app serves. The shared model calls the same thing scopeGroupIds.
func TestToInvitationJSONKeepsAllowedGroupIDsOnTheWire(t *testing.T) {
	t.Run("named groups carry over", func(t *testing.T) {
		out := toInvitationJSON(&models.RemoteInvitation{
			ID: "inv-2", ScopeGroupIDs: []string{"t_vans", "t_north"},
		})
		if len(out.AllowedGroupIDs) != 2 || out.AllowedGroupIDs[0] != "t_vans" {
			t.Fatalf("AllowedGroupIDs = %v; want the invite's groups", out.AllowedGroupIDs)
		}
	})

	// nil means full access and, with omitempty, is simply absent — which is
	// what the frontend already treats as unrestricted.
	t.Run("unrestricted stays absent", func(t *testing.T) {
		out := toInvitationJSON(&models.RemoteInvitation{ID: "inv-3"})
		if out.AllowedGroupIDs != nil {
			t.Errorf("AllowedGroupIDs = %v; want nil for an unrestricted invite", out.AllowedGroupIDs)
		}
	})
}
