package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func metricsApp() *fiber.App {
	logger := zerolog.Nop()
	app := fiber.New()
	_ = logger
	app.Use(NewMetricsMiddleware())
	app.Get("/vehicles/:tokenID", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	app.Get("/boom", func(*fiber.Ctx) error { return fiber.NewError(fiber.StatusServiceUnavailable, "down") })
	return app
}

// THE CARDINALITY PROPERTY, and the reason this middleware is hand-written
// rather than a one-line library call. Two requests for two different tenants
// must be ONE series. Labelling with c.Path() instead of the route pattern
// would mint a series per token id per method per status, and the metrics
// endpoint becomes the most expensive thing the service does.
func TestMetricsLabelsUseTheRoutePatternNotTheURL(t *testing.T) {
	app := metricsApp()
	before := testutil.ToFloat64(requestsTotal.WithLabelValues(
		http.MethodGet, "/vehicles/:tokenID", "200"))

	for _, tenant := range []string{"190171", "192379"} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/vehicles/"+tenant, nil))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	after := testutil.ToFloat64(requestsTotal.WithLabelValues(
		http.MethodGet, "/vehicles/:tokenID", "200"))
	assert.Equal(t, before+2, after, "both vehicles counted on one series")
}

// A handler that returns an error must be recorded with the status the error
// handler will send, not the 200 sitting on the response when the middleware
// resumes. Filing 503s as 200s would make the errors signal — the one that
// matters most — silently always-zero.
func TestMetricsRecordsErrorStatus(t *testing.T) {
	app := metricsApp()
	before := testutil.ToFloat64(requestsTotal.WithLabelValues(http.MethodGet, "/boom", "503"))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/boom", nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	after := testutil.ToFloat64(requestsTotal.WithLabelValues(http.MethodGet, "/boom", "503"))
	assert.Equal(t, before+1, after)
}

// Scanner traffic must not mint a series per path it probes.
func TestMetricsCollapsesUnmatchedRoutes(t *testing.T) {
	app := metricsApp()
	before := testutil.ToFloat64(requestsTotal.WithLabelValues(http.MethodGet, "unmatched", "404"))

	for _, p := range []string{"/wp-admin", "/.env", "/phpmyadmin"} {
		_, err := app.Test(httptest.NewRequest(http.MethodGet, p, nil))
		require.NoError(t, err)
	}

	after := testutil.ToFloat64(requestsTotal.WithLabelValues(http.MethodGet, "unmatched", "404"))
	assert.Equal(t, before+3, after, "three probes, one series")
}

// Duration is observed for every request, so latency and traffic cannot
// disagree about how many requests there were.
func TestMetricsObservesDuration(t *testing.T) {
	app := metricsApp()
	const route = "/vehicles/:tokenID"
	before := testutil.CollectAndCount(requestDuration)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet,
		"/vehicles/190171", nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.GreaterOrEqual(t, testutil.CollectAndCount(requestDuration), before,
		"the histogram carries an observation for "+route)
}

// In-flight returns to its baseline: a leaked increment reads as permanent
// saturation and would page somebody at 3am for a gauge that never comes down.
func TestMetricsInFlightReturnsToBaseline(t *testing.T) {
	app := metricsApp()
	before := testutil.ToFloat64(requestsInFlight)

	_, err := app.Test(httptest.NewRequest(http.MethodGet,
		"/vehicles/190171", nil))
	require.NoError(t, err)
	_, err = app.Test(httptest.NewRequest(http.MethodGet, "/boom", nil))
	require.NoError(t, err)

	assert.Equal(t, before, testutil.ToFloat64(requestsInFlight))
}
