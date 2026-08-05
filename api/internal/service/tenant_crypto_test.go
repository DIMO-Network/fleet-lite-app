package service

import (
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCryptoSvc(key string, allowLegacy bool) *TenantService {
	l := zerolog.Nop()
	return &TenantService{
		logger:   &l,
		settings: &config.Settings{TenantSecretEncKey: key, AllowLegacyEmptyEncKey: allowLegacy},
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	s := newCryptoSvc("a-real-key", false)
	enc, err := s.encryptSecret("super-secret-dimo-api-key")
	require.NoError(t, err)
	assert.NotContains(t, enc, "super-secret", "ciphertext must not leak plaintext")

	got, err := s.decryptSecret(enc)
	require.NoError(t, err)
	assert.Equal(t, "super-secret-dimo-api-key", got)
}

// The whole point of the fix: a value written while TENANT_SECRET_ENC_KEY was
// unset was encrypted under sha256(""), and the wrong key must not silently
// return garbage. GCM authenticates, so it fails cleanly.
func TestDecrypt_LegacyEmptyKey(t *testing.T) {
	legacy, err := EncryptSecretWith("", "written-when-the-key-was-unset")
	require.NoError(t, err)

	t.Run("fallback disabled: fails rather than guessing", func(t *testing.T) {
		_, err := newCryptoSvc("a-real-key", false).decryptSecret(legacy)
		assert.Error(t, err)
	})

	t.Run("fallback enabled: reads the legacy row", func(t *testing.T) {
		got, err := newCryptoSvc("a-real-key", true).decryptSecret(legacy)
		require.NoError(t, err)
		assert.Equal(t, "written-when-the-key-was-unset", got)
	})

	t.Run("fallback never applies when the primary key is itself empty", func(t *testing.T) {
		// Guard against the shim quietly re-enabling the weak key as primary.
		s := newCryptoSvc("", true)
		enc, err := s.encryptSecret("x")
		require.NoError(t, err)
		got, err := s.decryptSecret(enc)
		require.NoError(t, err)
		assert.Equal(t, "x", got, "empty primary still works locally, but Validate blocks it in prod")
	})
}

func TestDecrypt_WrongKeyFails(t *testing.T) {
	enc, err := EncryptSecretWith("key-one", "payload")
	require.NoError(t, err)
	_, err = newCryptoSvc("key-two", true).decryptSecret(enc)
	assert.Error(t, err, "a wrong key must error, not return garbage")
}

func TestDecrypt_EmptyInputIsNotAnError(t *testing.T) {
	got, err := newCryptoSvc("k", false).decryptSecret("")
	require.NoError(t, err)
	assert.Empty(t, got)
}
