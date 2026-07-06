package service

import (
	"testing"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
)

func TestLatestPlate(t *testing.T) {
	base := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	rfc := func(tm time.Time) string { return tm.Format(time.RFC3339) }

	reg := func(tm time.Time, data string) gateway.AttestationEntry {
		return gateway.AttestationEntry{Type: VehicleRegistrationCloudEventType, Time: rfc(tm), Data: []byte(data)}
	}

	cases := []struct {
		name      string
		entries   []gateway.AttestationEntry
		wantPlate string
		wantFound bool
	}{
		{
			name:      "no entries",
			entries:   nil,
			wantFound: false,
		},
		{
			name:      "single plate",
			entries:   []gateway.AttestationEntry{reg(base, `{"license_plate":"ABC123"}`)},
			wantPlate: "ABC123",
			wantFound: true,
		},
		{
			name: "latest by time wins",
			entries: []gateway.AttestationEntry{
				reg(base, `{"license_plate":"OLD111"}`),
				reg(base.Add(time.Hour), `{"license_plate":"NEW222"}`),
				reg(base.Add(-time.Hour), `{"license_plate":"OLDER0"}`),
			},
			wantPlate: "NEW222",
			wantFound: true,
		},
		{
			name: "plateless and wrong-type entries ignored",
			entries: []gateway.AttestationEntry{
				{Type: "dimo.document.vehicle.groups", Time: rfc(base.Add(2 * time.Hour)), Data: []byte(`{"groups":[]}`)},
				reg(base.Add(time.Hour), `{"vin":"1ABC"}`), // registration doc, no plate field
				reg(base, `{"license_plate":"PLT123"}`),
			},
			wantPlate: "PLT123",
			wantFound: true,
		},
		{
			name:      "empty/whitespace plate ignored",
			entries:   []gateway.AttestationEntry{reg(base, `{"license_plate":"  "}`)},
			wantFound: false,
		},
		{
			name: "newer doc without plate does not override older plate",
			entries: []gateway.AttestationEntry{
				reg(base.Add(time.Hour), `{"vin":"only-vin"}`),
				reg(base, `{"license_plate":"KEEPME"}`),
			},
			wantPlate: "KEEPME",
			wantFound: true,
		},
		{
			name: "nested extract-api shape with plateNumber alias",
			entries: []gateway.AttestationEntry{
				reg(base, `{"data":{"fields":{"plateNumber":"8WSK941","vin":"JTJGARBZ0M5023425"}},"type":"dimo.document.vehicle.registration"}`),
			},
			wantPlate: "8WSK941",
			wantFound: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plate, found := latestRegistrationField(c.entries, licensePlateFieldNames)
			if found != c.wantFound || plate != c.wantPlate {
				t.Fatalf("latestRegistrationField(license_plate) = (%q, %v), want (%q, %v)", plate, found, c.wantPlate, c.wantFound)
			}
		})
	}
}

func TestLatestRegistrationFieldVIN(t *testing.T) {
	base := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	rfc := func(tm time.Time) string { return tm.Format(time.RFC3339) }
	reg := func(tm time.Time, data string) gateway.AttestationEntry {
		return gateway.AttestationEntry{Type: VehicleRegistrationCloudEventType, Time: rfc(tm), Data: []byte(data)}
	}

	cases := []struct {
		name      string
		entries   []gateway.AttestationEntry
		wantVIN   string
		wantFound bool
	}{
		{name: "no entries", entries: nil, wantFound: false},
		{
			name:      "single vin",
			entries:   []gateway.AttestationEntry{reg(base, `{"vin":"1ABC"}`)},
			wantVIN:   "1ABC",
			wantFound: true,
		},
		{
			name: "latest by time wins",
			entries: []gateway.AttestationEntry{
				reg(base, `{"vin":"OLDVIN"}`),
				reg(base.Add(time.Hour), `{"vin":"NEWVIN"}`),
			},
			wantVIN:   "NEWVIN",
			wantFound: true,
		},
		{
			name: "vin read independently of plate-only docs",
			entries: []gateway.AttestationEntry{
				reg(base.Add(time.Hour), `{"license_plate":"PLT123"}`), // newer, no vin
				reg(base, `{"vin":"KEEPVIN"}`),
			},
			wantVIN:   "KEEPVIN",
			wantFound: true,
		},
		{
			name: "nested extract-api shape",
			entries: []gateway.AttestationEntry{
				reg(base, `{"data":{"fields":{"plateNumber":"8WSK941","vin":"JTJGARBZ0M5023425"}},"type":"dimo.document.vehicle.registration"}`),
			},
			wantVIN:   "JTJGARBZ0M5023425",
			wantFound: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vin, found := latestRegistrationField(c.entries, vinFieldNames)
			if found != c.wantFound || vin != c.wantVIN {
				t.Fatalf("latestRegistrationField(vin) = (%q, %v), want (%q, %v)", vin, found, c.wantVIN, c.wantFound)
			}
		})
	}
}
