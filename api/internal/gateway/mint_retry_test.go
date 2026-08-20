package gateway

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flakyLocalMinter fails its first failUntil calls and succeeds after.
type flakyLocalMinter struct {
	failUntil int
	calls     int
}

func (f *flakyLocalMinter) GetToken() *jwt.Token {
	f.calls++
	if f.calls <= f.failUntil {
		return nil
	}
	return &jwt.Token{Raw: "minted"}
}

// The case this exists for: the challenge flakes once and the next fresh one
// works. Before the retry this failed the nightly diff and 500'd a page load.
func TestMintWithRetrySucceedsAfterOneFlake(t *testing.T) {
	m := &flakyLocalMinter{failUntil: 1}
	var retried []int

	token := mintWithRetry(m, func(attempt int) { retried = append(retried, attempt) })

	require.NotNil(t, token)
	assert.Equal(t, "minted", token.Raw)
	assert.Equal(t, 2, m.calls, "one failure, one fresh challenge")
	assert.Equal(t, []int{1}, retried, "the retry is logged, not silent")
}

// A credential that is genuinely wrong must still fail, and quickly.
func TestMintWithRetryGivesUp(t *testing.T) {
	m := &flakyLocalMinter{failUntil: 99}

	assert.Nil(t, mintWithRetry(m, nil))
	assert.Equal(t, mintAttempts, m.calls, "bounded, not forever")
}

// The happy path must not pay for the retry.
func TestMintWithRetryFirstAttemptWins(t *testing.T) {
	m := &flakyLocalMinter{}
	retried := false

	require.NotNil(t, mintWithRetry(m, func(int) { retried = true }))
	assert.Equal(t, 1, m.calls)
	assert.False(t, retried)
}

// The last attempt must be a real attempt, not a spare.
func TestMintWithRetryUsesEveryAttempt(t *testing.T) {
	m := &flakyLocalMinter{failUntil: mintAttempts - 1}

	require.NotNil(t, mintWithRetry(m, nil))
	assert.Equal(t, mintAttempts, m.calls)
}
