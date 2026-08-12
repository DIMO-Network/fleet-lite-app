package service

import (
	"testing"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/aarondl/null/v8"
	"github.com/rs/zerolog"
)

func TestRemovalAllowed(t *testing.T) {
	base := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	rfc := func(tm time.Time) string { return tm.Format(time.RFC3339) }

	ours := func(tm time.Time) gateway.AttestationEntry {
		return gateway.AttestationEntry{Producer: GroupAttestationProducer, Time: rfc(tm)}
	}
	foreign := func(tm time.Time) gateway.AttestationEntry {
		return gateway.AttestationEntry{Producer: "0xSomeoneElse", Time: rfc(tm)}
	}

	cases := []struct {
		name          string
		entries       []gateway.AttestationEntry
		groupsUpdated null.Time
		want          bool
	}{
		{
			name:          "no local change (groups_updated_at NULL) -> gate open",
			entries:       []gateway.AttestationEntry{foreign(base)},
			groupsUpdated: null.Time{},
			want:          true,
		},
		{
			name:          "our CE caught up (>= groups_updated_at) -> gate open",
			entries:       []gateway.AttestationEntry{ours(base)},
			groupsUpdated: null.TimeFrom(base.Add(-2 * time.Second)),
			want:          true,
		},
		{
			name:          "our CE exactly at groups_updated_at -> gate open",
			entries:       []gateway.AttestationEntry{ours(base)},
			groupsUpdated: null.TimeFrom(base),
			want:          true,
		},
		{
			name:          "our CE stale (behind groups_updated_at) -> gate closed",
			entries:       []gateway.AttestationEntry{ours(base.Add(-10 * time.Second))},
			groupsUpdated: null.TimeFrom(base),
			want:          false,
		},
		{
			name:          "local change but no producer CE of ours -> gate closed",
			entries:       []gateway.AttestationEntry{foreign(base.Add(time.Hour))},
			groupsUpdated: null.TimeFrom(base),
			want:          false,
		},
		{
			name:          "latest of several of our CEs wins -> gate open",
			entries:       []gateway.AttestationEntry{ours(base.Add(-time.Hour)), ours(base.Add(time.Minute)), foreign(base)},
			groupsUpdated: null.TimeFrom(base),
			want:          true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := removalAllowed(tc.entries, tc.groupsUpdated); got != tc.want {
				t.Errorf("removalAllowed() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The create and update paths both resolve metadata through this, so a group
// cannot be created with one name and then "updated" to a different one from
// the same attestation.
func TestResolveGroupMetadata(t *testing.T) {
	for _, tc := range []struct {
		name      string
		in        models.GroupRef
		wantName  string
		wantColor string
	}{
		{
			name:      "name and colour pass through",
			in:        models.GroupRef{ID: "t_a", Name: "Detalle de automóviles", Color: "#ff0000"},
			wantName:  "Detalle de automóviles",
			wantColor: "#ff0000",
		},
		{
			name:      "blank name falls back to the id",
			in:        models.GroupRef{ID: "t_a", Name: "   "},
			wantName:  "t_a",
			wantColor: "#808080",
		},
		{
			name:      "surrounding whitespace is trimmed",
			in:        models.GroupRef{ID: "t_a", Name: "  Logística Pajaritos  ", Color: "#123456"},
			wantName:  "Logística Pajaritos",
			wantColor: "#123456",
		},
		{
			name:      "missing colour defaults to neutral gray",
			in:        models.GroupRef{ID: "t_a", Name: "Fleet"},
			wantName:  "Fleet",
			wantColor: "#808080",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotColor := resolveGroupMetadata(tc.in)
			if gotName != tc.wantName {
				t.Fatalf("name: got %q, want %q", gotName, tc.wantName)
			}
			if gotColor != tc.wantColor {
				t.Fatalf("color: got %q, want %q", gotColor, tc.wantColor)
			}
		})
	}
}

// A renamed group must be detected as changed. Before this, ensureGroup returned
// early on existence and a rename at the source could never reach us: the row
// kept whatever name the first attestation carried, and the reconcile reported
// changed=0 because names were never compared.
func TestGroupMetadataChangeIsDetected(t *testing.T) {
	stored := struct{ name, color string }{"TOTAL DE UNIDADES", "#808080"}
	incoming := models.GroupRef{ID: "t_a", Name: "Detalle de automóviles"}

	name, color := resolveGroupMetadata(incoming)
	if stored.name == name && stored.color == color {
		t.Fatal("a renamed group must not compare equal to its stored row")
	}

	// And an unchanged group must not churn the row on every import.
	same := models.GroupRef{ID: "t_a", Name: "TOTAL DE UNIDADES"}
	n2, c2 := resolveGroupMetadata(same)
	if stored.name != n2 || stored.color != c2 {
		t.Fatalf("unchanged group should compare equal, got %q/%q", n2, c2)
	}
}

// Group metadata rides on every member vehicle's attestation, so members
// attested before and after a rename disagree. desiredGroups must surface the
// newest, not whichever producer the map happened to yield last — otherwise the
// stored name becomes a function of iteration order.
func TestDesiredGroupsPrefersNewerAttestationForMetadata(t *testing.T) {
	older := `{"groups":[{"id":"t_a","name":"TOTAL DE UNIDADES","color":"#808080"}]}`
	newer := `{"groups":[{"id":"t_a","name":"Detalle de automóviles","color":"#808080"}]}`

	entries := []gateway.AttestationEntry{
		{Type: VehicleGroupsCloudEventType, Producer: "p1", Source: "p1",
			Time: "2026-08-11T19:01:00Z", Data: []byte(older)},
		{Type: VehicleGroupsCloudEventType, Producer: "p2", Source: "p2",
			Time: "2026-08-12T11:18:00Z", Data: []byte(newer)},
	}

	got := desiredGroups(entries, zerolog.Nop(), 1, "t", false)
	if len(got) != 1 {
		t.Fatalf("expected one group, got %d", len(got))
	}
	if got[0].Name != "Detalle de automóviles" {
		t.Fatalf("newer attestation must win, got %q", got[0].Name)
	}
	if got[0].AttestedAt.IsZero() {
		t.Fatal("AttestedAt must be stamped so ensureGroup can compare against the row")
	}

	// Order of entries must not change the answer.
	entries[0], entries[1] = entries[1], entries[0]
	got = desiredGroups(entries, zerolog.Nop(), 1, "t", false)
	if got[0].Name != "Detalle de automóviles" {
		t.Fatalf("result must not depend on iteration order, got %q", got[0].Name)
	}
}
