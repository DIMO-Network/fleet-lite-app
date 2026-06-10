package gateway

import (
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

func signedToken(t *testing.T, claims jwtlib.MapClaims) string {
	t.Helper()
	tok := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	raw, err := tok.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return raw
}

func TestCacheTTLFromJWT(t *testing.T) {
	const fallback = 10 * time.Minute
	const margin = time.Minute

	t.Run("uses exp minus margin", func(t *testing.T) {
		raw := signedToken(t, jwtlib.MapClaims{"exp": time.Now().Add(time.Hour).Unix()})
		ttl := cacheTTLFromJWT(raw, margin, fallback)
		want := time.Hour - margin
		// Allow a little slack for test execution time.
		if ttl < want-5*time.Second || ttl > want {
			t.Errorf("ttl = %v, want ~%v", ttl, want)
		}
	})

	t.Run("falls back when exp missing", func(t *testing.T) {
		raw := signedToken(t, jwtlib.MapClaims{"sub": "x"})
		if ttl := cacheTTLFromJWT(raw, margin, fallback); ttl != fallback {
			t.Errorf("ttl = %v, want fallback %v", ttl, fallback)
		}
	})

	t.Run("falls back when exp inside margin or past", func(t *testing.T) {
		raw := signedToken(t, jwtlib.MapClaims{"exp": time.Now().Add(30 * time.Second).Unix()})
		if ttl := cacheTTLFromJWT(raw, margin, fallback); ttl != fallback {
			t.Errorf("ttl = %v, want fallback %v", ttl, fallback)
		}
	})

	t.Run("falls back on garbage input", func(t *testing.T) {
		if ttl := cacheTTLFromJWT("not-a-jwt", margin, fallback); ttl != fallback {
			t.Errorf("ttl = %v, want fallback %v", ttl, fallback)
		}
	})
}
