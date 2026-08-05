package config

import "testing"

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

func TestValidate_KeySetPasses(t *testing.T) {
	if err := (&Settings{Environment: "prod", TenantSecretEncKey: "k"}).Validate(); err != nil {
		t.Errorf("want no error with a key set, got %v", err)
	}
}
