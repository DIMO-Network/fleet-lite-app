package app

import (
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// The four golden signals for this service's HTTP surface.
//
// Metric and label names deliberately match the cluster's existing
// `go-fiber-dashboard.json` (`grafana-dashboards-ext`, from
// cluster-helm-charts/charts/dimo-mon) and fleet-tenancy-api's identical
// middleware.
//
// THIS APP WAS ALREADY "INSTRUMENTED" AND ENTIRELY BLIND, which is worth
// knowing before someone concludes this file duplicates work.
// shared/pkg/middleware/metrics.HTTPMetricsMiddleware runs alongside it and
// emits `device_data_api_http_request_count` — named after a different service
// in all ten repos that use it — labelled `path` with `c.Route().Name`, which
// is empty unless a route was explicitly named. Prod serves exactly one series
// for the whole app:
//
//	device_data_api_http_request_count{method="GET",path="",status="200"} 356
//
// One number for the fleet list, telemetry, glovebox and health together. No
// dashboard and no alert rule reads it, in any service. It is left registered
// rather than removed — nine sibling services emit it and diverging here buys
// nothing — but it is not what to chart.
//
//	latency     http_request_duration_seconds  (histogram)
//	traffic     http_requests_total            (counter)
//	errors      http_requests_total{status=5xx}
//	saturation  http_requests_in_flight        (gauge)
var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests, by method, route pattern and status code.",
	}, []string{"method", "path", "status"})

	// Buckets keep the long tail rather than the client default's, because
	// this app's slow paths are slow for real reasons: a fleet render fans out
	// to fleet-tenancy-api for three gates and the roster, and the telemetry
	// and glovebox routes wait on other DIMO services. A p99 that saturates the
	// top bucket tells you nothing about which of them regressed.
	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration in seconds, by method, route pattern and status code.",
		Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"method", "path", "status"})

	requestsInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "HTTP requests currently being served.",
	})
)

// NewMetricsMiddleware records one observation per request.
//
// THE LABEL IS THE ROUTE PATTERN, NEVER THE URL. `c.Route().Path` gives
// `/v1/tenants/:tenantId/vehicles`; `c.Path()` would give a distinct label
// value per tenant uuid, so a few hundred customers would become a few hundred
// series per method per status — the classic way a metrics endpoint becomes the
// most expensive thing a service does. Unmatched requests collapse to a single
// "unmatched" bucket for the same reason: a 404 is usually a scanner walking
// paths, and each miss would otherwise mint a permanent series.
func NewMetricsMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		requestsInFlight.Inc()
		defer requestsInFlight.Dec()

		// Run the rest of the chain first: the error handler converts a
		// returned error into a status code, and observing before that would
		// record every failure as a 200.
		err := c.Next()

		status := c.Response().StatusCode()
		if err != nil {
			// The handler returned an error the ErrorHandler has not written
			// yet. Read the status it will use, so a 503 is not filed as 200.
			var fe *fiber.Error
			if errors.As(err, &fe) {
				status = fe.Code
			} else {
				status = fiber.StatusInternalServerError
			}
		}

		labels := prometheus.Labels{
			"method": c.Method(),
			"path":   routeLabel(c),
			"status": strconv.Itoa(status),
		}
		requestsTotal.With(labels).Inc()
		requestDuration.With(labels).Observe(time.Since(start).Seconds())
		return err
	}
}

// routeLabel is the registered route pattern, or "unmatched".
func routeLabel(c *fiber.Ctx) string {
	if r := c.Route(); r != nil && r.Path != "" {
		// Fiber reports "/" for unmatched paths on some versions; treat a "/"
		// route label on a non-root request as unmatched rather than merging
		// scanner traffic into the root route's numbers.
		if r.Path == "/" && c.Path() != "/" {
			return "unmatched"
		}
		return r.Path
	}
	return "unmatched"
}
