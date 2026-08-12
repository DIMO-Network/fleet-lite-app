package service

import (
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCryptoSvc(key string) *TenantService {
	l := zerolog.Nop()
	return &TenantService{
		logger:   &l,
		settings: &config.Settings{TenantSecretEncKey: key},
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	s := newCryptoSvc("a-real-key")
	enc, err := s.encryptSecret("super-secret-dimo-api-key")
	require.NoError(t, err)
	assert.NotContains(t, enc, "super-secret", "ciphertext must not leak plaintext")

	got, err := s.decryptSecret(enc)
	require.NoError(t, err)
	assert.Equal(t, "super-secret-dimo-api-key", got)
}

// A value written while TENANT_SECRET_ENC_KEY was unset was encrypted under
// sha256(""), a constant anyone can compute. The fallback that used to read
// those rows is gone: keeping it left the weak key a valid way to read every
// credential. Such a row must now fail cleanly rather than be silently
// readable — recovery goes through reencrypt-tenant-secrets -from-empty-key.
func TestDecrypt_LegacyEmptyKeyIsRefused(t *testing.T) {
	legacy, err := EncryptSecretWith("", "written-when-the-key-was-unset")
	require.NoError(t, err)

	_, err = newCryptoSvc("a-real-key").decryptSecret(legacy)
	assert.Error(t, err, "the weak key must no longer be a way in")

	// The recovery path the CLI uses still reads it, so no data is stranded.
	got, err := DecryptSecretWith("", legacy)
	require.NoError(t, err)
	assert.Equal(t, "written-when-the-key-was-unset", got)
}

func TestDecrypt_WrongKeyFails(t *testing.T) {
	enc, err := EncryptSecretWith("key-one", "payload")
	require.NoError(t, err)
	_, err = newCryptoSvc("key-two").decryptSecret(enc)
	assert.Error(t, err, "a wrong key must error, not return garbage")
}

func TestDecrypt_EmptyInputIsNotAnError(t *testing.T) {
	got, err := newCryptoSvc("k").decryptSecret("")
	require.NoError(t, err)
	assert.Empty(t, got)
}
