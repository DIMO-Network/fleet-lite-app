package service

import "testing"

func TestParseVINVCResponse(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantVIN   string
		wantFound bool
		wantErr   bool
	}{
		{
			name:      "vc present",
			raw:       `{"data":{"vinVCLatest":{"vin":"JTJGARBZ0M5023425"}}}`,
			wantVIN:   "JTJGARBZ0M5023425",
			wantFound: true,
		},
		{
			name:      "no vc (null)",
			raw:       `{"data":{"vinVCLatest":null}}`,
			wantFound: false,
		},
		{
			name:      "empty data object",
			raw:       `{"data":{}}`,
			wantFound: false,
		},
		{
			name:      "blank vin treated as absent",
			raw:       `{"data":{"vinVCLatest":{"vin":"   "}}}`,
			wantFound: false,
		},
		{
			name:      "vin whitespace trimmed",
			raw:       `{"data":{"vinVCLatest":{"vin":" 1ABC123 "}}}`,
			wantVIN:   "1ABC123",
			wantFound: true,
		},
		{
			name:    "malformed body errors",
			raw:     `not json`,
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vin, found, err := parseVINVCResponse([]byte(c.raw))
			if (err != nil) != c.wantErr {
				t.Fatalf("parseVINVCResponse err = %v, wantErr %v", err, c.wantErr)
			}
			if found != c.wantFound || vin != c.wantVIN {
				t.Fatalf("parseVINVCResponse = (%q, %v), want (%q, %v)", vin, found, c.wantVIN, c.wantFound)
			}
		})
	}
}
