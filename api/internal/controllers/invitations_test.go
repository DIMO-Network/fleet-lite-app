package controllers

import (
	"testing"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/aarondl/null/v8"
)

// The Members settings view renders a delivery badge straight off these fields,
// and it distinguishes "sent but unconfirmed" from "never dispatched" by the
// presence of emailStatus. So an unsent invitation must serialize with all three
// tracking fields absent (nil), never as empty strings — an empty-but-present
// status would render as an unknown state instead of "Not sent".
func TestToInvitationJSONEmailTracking(t *testing.T) {
	base := func() *dbmodels.Invitation {
		return &dbmodels.Invitation{
			ID:        "inv-1",
			Email:     "someone@example.com",
			Role:      "member",
			Status:    "pending",
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(time.Hour),
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

	t.Run("valid-but-empty status is treated as absent", func(t *testing.T) {
		inv := base()
		inv.EmailStatus = null.StringFrom("")
		if out := toInvitationJSON(inv); out.EmailStatus != nil {
			t.Errorf("EmailStatus = %q; want nil for an empty status", *out.EmailStatus)
		}
	})

	t.Run("bounced carries status, timestamp and reason", func(t *testing.T) {
		at := time.Date(2026, 8, 1, 12, 30, 0, 0, time.UTC)
		inv := base()
		inv.EmailStatus = null.StringFrom("bounced")
		inv.EmailStatusAt = null.TimeFrom(at)
		inv.EmailStatusDetail = null.StringFrom("HardBounce: address does not exist")

		out := toInvitationJSON(inv)
		if out.EmailStatus == nil || *out.EmailStatus != "bounced" {
			t.Fatalf("EmailStatus = %v; want bounced", out.EmailStatus)
		}
		if out.EmailStatusAt == nil || *out.EmailStatusAt != at.Format(time.RFC3339) {
			t.Errorf("EmailStatusAt = %v; want %s", out.EmailStatusAt, at.Format(time.RFC3339))
		}
		if out.EmailStatusDetail == nil || *out.EmailStatusDetail != "HardBounce: address does not exist" {
			t.Errorf("EmailStatusDetail = %v; want the bounce reason", out.EmailStatusDetail)
		}
	})

	t.Run("timestamp is emitted in UTC RFC3339 regardless of stored zone", func(t *testing.T) {
		zone := time.FixedZone("UTC-5", -5*3600)
		at := time.Date(2026, 8, 1, 7, 30, 0, 0, zone)
		inv := base()
		inv.EmailStatus = null.StringFrom("delivered")
		inv.EmailStatusAt = null.TimeFrom(at)

		out := toInvitationJSON(inv)
		if want := "2026-08-01T12:30:00Z"; out.EmailStatusAt == nil || *out.EmailStatusAt != want {
			t.Errorf("EmailStatusAt = %v; want %s", out.EmailStatusAt, want)
		}
	})
}
