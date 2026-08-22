package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
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

// THE BUG THIS PINS took this app's /metrics to a 500 in production on
// 2026-08-22, firing TargetDown and hiding the whole service from Grafana
// while it served traffic perfectly well. fleet-tenancy-api's identical
// middleware had done the same thing the day before; this file was cloned from
// it before the fix landed there.
//
// fiber's c.Method() is a zero-copy view over fasthttp's request buffer, and
// that buffer is reused for a later request. Prometheus retains label strings
// inside its registry, so a retained view mutates after the fact: the series
// becomes method="GETT", collides with the real one, and every scrape fails
// with "collected before with the same name and label values".
//
// The test mutates the buffer directly rather than racing real requests,
// because the real failure is timing-dependent and a flaky guard against a
// silent outage is worse than none.
func TestMetricsMethodLabelDoesNotAliasTheRequestBuffer(t *testing.T) {
	app := fiber.New()
	fctx := &fasthttp.RequestCtx{}
	fctx.Request.Header.SetMethod("GET")
	c := app.AcquireCtx(fctx)
	defer app.ReleaseCtx(c)

	label := utils.CopyString(c.Method())

	// What fasthttp does between requests: the same backing array, new bytes.
	fctx.Request.Header.SetMethod("DELETE")

	assert.Equal(t, "GET", label,
		"the stored label must survive the request buffer being reused; without a copy it mutates into garbage and poisons the registry")
}

// The observable consequence, end to end: after traffic, every method label in
// the registry is a real HTTP method. A corrupted label ("GETT") is what makes
// /metrics answer 500 rather than serving numbers.
func TestMetricsEmitsOnlyRealMethodLabels(t *testing.T) {
	app := metricsApp()
	for i := 0; i < 25; i++ {
		for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			req := httptest.NewRequest(m, "/vehicles/190171", nil)
			_, err := app.Test(req)
			require.NoError(t, err)
		}
	}

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err, "a poisoned registry fails to gather — which is the 500 the scraper sees")

	valid := map[string]bool{
		http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
		http.MethodPatch: true, http.MethodDelete: true, http.MethodHead: true,
		http.MethodOptions: true, http.MethodConnect: true, http.MethodTrace: true,
	}
	for _, fam := range families {
		if fam.GetName() != "http_requests_total" {
			continue
		}
		for _, m := range fam.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "method" {
					assert.True(t, valid[l.GetValue()],
						"label method=%q is not an HTTP method — the request buffer leaked into the registry", l.GetValue())
				}
			}
		}
	}
}
