package service

import (
	"testing"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/aarondl/null/v8"
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
