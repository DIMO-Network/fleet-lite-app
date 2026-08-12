package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The level matters more than the message. Since authorization moved to
// fleet-tenancy-api, a 403 is the ordinary answer for a wallet that is not a
// member of the requested tenant — logging that at error level makes routine
// enforcement indistinguishable from the app being broken.
func TestErrorHandlerLogLevels(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		message   string
		wantLevel string
		wantLog   bool
	}{
		{"forbidden is a warning", fiber.StatusForbidden, "no access to tenant", "warn", true},
		{"unauthorized is a warning", fiber.StatusUnauthorized, "invalid token", "warn", true},
		{"bad request is a warning", fiber.StatusBadRequest, "Tenant-Id header is required", "warn", true},
		{"service unavailable stays an error", fiber.StatusServiceUnavailable, "authorization service unavailable", "error", true},
		{"server error stays an error", fiber.StatusInternalServerError, "failed to load tenant", "error", true},
		{"bad gateway stays an error", fiber.StatusBadGateway, "failed to sync vehicle groups", "error", true},
		// An unrouted path is neither a fault nor worth a line per scan.
		{"not found is silent", fiber.StatusNotFound, "nope", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := zerolog.New(&buf)

			app := fiber.New(fiber.Config{
				ErrorHandler: func(c *fiber.Ctx, err error) error { return ErrorHandler(c, err, &l) },
			})
			app.Get("/boom", func(_ *fiber.Ctx) error { return fiber.NewError(tc.status, tc.message) })

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/boom", nil))
			require.NoError(t, err)
			assert.Equal(t, tc.status, resp.StatusCode)

			if !tc.wantLog {
				assert.Empty(t, buf.String(), "expected no log line")
				return
			}
			var entry map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &entry), "log line: %s", buf.String())
			assert.Equal(t, tc.wantLevel, entry["level"])
			assert.Equal(t, tc.message, entry["error"])
			assert.Equal(t, "/boom", entry["httpPath"])
		})
	}
}

// The response body is unchanged by the level: clients and the tenancy clients
// both key off it.
func TestErrorHandlerResponseBody(t *testing.T) {
	l := zerolog.Nop()
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error { return ErrorHandler(c, err, &l) },
	})
	app.Get("/boom", func(_ *fiber.Ctx) error { return fiber.NewError(fiber.StatusForbidden, "no access to tenant") })

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/boom", nil))
	require.NoError(t, err)

	var body ErrorRes
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, fiber.StatusForbidden, body.Code)
	assert.Equal(t, "no access to tenant", body.Message)
	assert.Equal(t, fiber.MIMEApplicationJSON, resp.Header.Get(fiber.HeaderContentType))
}
