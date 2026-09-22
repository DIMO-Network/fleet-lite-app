package service

import (
	"encoding/json"
	"testing"
)

func TestFlexBool_UnmarshalJSON(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    bool
		wantErr bool
	}{
		{"native true", `true`, true, false},
		{"native false", `false`, false, false},
		{"numeric zero", `0`, false, false},
		{"numeric one", `1`, true, false},
		{"numeric other nonzero", `2`, true, false},
		{"invalid string", `"charging"`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b flexBool
			err := json.Unmarshal([]byte(tc.raw), &b)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got nil error, want one")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bool(b) != tc.want {
				t.Fatalf("got %v, want %v", bool(b), tc.want)
			}
		})
	}
}

func TestFlexBool_NilPointerOnJSONNull(t *testing.T) {
	var target struct {
		V *flexBool `json:"v"`
	}
	if err := json.Unmarshal([]byte(`{"v": null}`), &target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.V != nil {
		t.Fatalf("got %v, want nil (JSON null must not invoke UnmarshalJSON)", target.V)
	}
}

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
