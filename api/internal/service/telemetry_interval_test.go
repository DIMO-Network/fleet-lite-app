package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeInterval(t *testing.T) {
	cases := map[string]string{
		"1d":  "24h",
		"7d":  "168h",
		"24h": "24h",
		"1h":  "1h",
		"15m": "15m",
		"3s":  "3s",
		"d":   "d",
		"":    "",
	}
	for in, want := range cases {
		assert.Equal(t, want, normalizeInterval(in), "interval %q", in)
	}
}
