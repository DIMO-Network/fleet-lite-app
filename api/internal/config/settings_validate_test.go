package config

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// An empty TENANT_SECRET_ENC_KEY is not "no encryption" — sha256("") is a valid
// AES-256 key, so encryption succeeds under a constant anyone can compute, with
// nothing errored and nothing logged. It reached production. Validate is the
// only place it can be caught.
func TestValidate_EmptyEncKey(t *testing.T) {
	for _, tc := range []struct {
		env     string
		wantErr bool
	}{
		{"prod", true},
		{"dev", true},
		{"local", false},
		// IsLocal() is "local" exactly — anything else, including "localdev",
		// fails closed. Deliberate: a typo'd environment should refuse to boot
		// rather than quietly encrypt under the weak key.
		{"localdev", true},
	} {
		err := (&Settings{Environment: tc.env}).Validate()
		if tc.wantErr && err == nil {
			t.Errorf("env %q: want error for empty key, got nil", tc.env)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("env %q: want no error, got %v", tc.env, err)
		}
	}
}

var prodClientID = common.HexToAddress("0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB")

func TestValidate_KeySetPasses(t *testing.T) {
	if err := (&Settings{Environment: "prod", TenantSecretEncKey: "k", DimoAuthClientID: prodClientID}).Validate(); err != nil {
		t.Errorf("want no error with a key set, got %v", err)
	}
}

// The client id is what a JWT's audience is checked against. Unset, the check
// is skipped and any DIMO app's token is accepted: tolerable where nobody can
// sign in (dev, local), a hole in prod.
func TestValidate_ClientIDRequiredInProd(t *testing.T) {
	for _, tc := range []struct {
		env      string
		clientID common.Address
		wantErr  bool
	}{
		{"prod", common.Address{}, true},
		{"prod", prodClientID, false},
		{"dev", common.Address{}, false},
		{"local", common.Address{}, false},
	} {
		err := (&Settings{Environment: tc.env, TenantSecretEncKey: "k", DimoAuthClientID: tc.clientID}).Validate()
		if tc.wantErr && err == nil {
			t.Errorf("env %q client %s: want error, got nil", tc.env, tc.clientID.Hex())
		}
		if !tc.wantErr && err != nil {
			t.Errorf("env %q client %s: want no error, got %v", tc.env, tc.clientID.Hex(), err)
		}
	}
}
