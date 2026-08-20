package gateway

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// tokenMinter is the one method this package needs from dimoauth.AuthService,
// narrowed so the retry below is testable without an auth server.
type tokenMinter interface {
	GetToken() *jwt.Token
}

// mintAttempts is how many times a mint is tried before it is called a
// failure, and mintBackoff is the pause between attempts.
//
// Three, not more: this runs on the request path behind every fleet page, so
// the worst case must stay inside a human's patience.
const (
	mintAttempts = 3
	mintBackoff  = 250 * time.Millisecond
)

// mintWithRetry gets a developer JWT, retrying a nil result.
//
// WHY THIS EXISTS, because it looks like the kind of retry that papers over a
// real error. On 2026-08-20 the nightly groups-diff failed on
// `submit_challenge` with 400 "Could not verify signature" for tenant
// e0cd30da; a re-run minted that tenant fine and failed on a DIFFERENT tenant,
// and three runs after that were clean. The keys are right — identity-api
// confirms each licence's signer is unchanged, and the same key mints
// successfully seconds later. It is the login challenge that is unreliable,
// roughly one attempt in fourteen.
//
// A retry is the correct response specifically because the challenge is
// SINGLE-USE. `dimoauth` already retries the two HTTP calls individually
// (shttp.WithRetry(3)), which cannot help and may well be the cause:
// re-submitting a consumed or unknown `state` is exactly what "could not
// verify signature" looks like from outside. Only a fresh challenge can
// succeed, and `GetToken` starts one on every call — so calling it again is a
// new attempt, not the same request repeated.
//
// It is bounded and it does not swallow the failure: nil still comes back
// after the last attempt and the caller still errors, so a credential that is
// genuinely wrong fails in about half a second rather than hanging.
func mintWithRetry(minter tokenMinter, onRetry func(attempt int)) *jwt.Token {
	for attempt := 1; ; attempt++ {
		if token := minter.GetToken(); token != nil {
			return token
		}
		if attempt >= mintAttempts {
			return nil
		}
		if onRetry != nil {
			onRetry(attempt)
		}
		time.Sleep(mintBackoff)
	}
}
