package main

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Each router owns its registry, so tests and independent servers do not share
// counters. Labels contain bounded route templates, never product IDs or keys.
type requestMetrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	errors   *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newRequestMetrics() *requestMetrics {
	m := &requestMetrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "product_http_requests_total",
			Help: "Completed HTTP requests, excluding Prometheus scrapes.",
		}, []string{"method", "endpoint", "status"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "product_http_errors_total",
			Help: "Completed HTTP requests with a non-2xx status, excluding scrapes.",
		}, []string{"method", "endpoint"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "product_http_request_duration_seconds",
			Help: "Gin request duration in seconds, excluding scrapes and pre-handler network/queue time.",
			Buckets: []float64{0.00001, 0.000025, 0.00005, 0.0001, 0.00025, 0.0005,
				0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "endpoint"}),
	}
	m.registry.MustRegister(m.requests, m.errors, m.duration,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	// Export zero-valued series before traffic so idle RPS/errors are visible.
	for _, prefix := range []string{"/v1", ""} {
		m.seed("GET", prefix+"/products/:productId", 200, 401, 404, 500)
		m.seed("POST", prefix+"/products/:productId/details", 204, 400, 401, 404, 500)
	}
	m.seed("GET", "/health", 200)
	return m
}

func (m *requestMetrics) seed(method, endpoint string, statuses ...int) {
	for _, status := range statuses {
		m.requests.WithLabelValues(method, endpoint, strconv.Itoa(status)).Add(0)
	}
	m.errors.WithLabelValues(method, endpoint).Add(0)
	m.duration.WithLabelValues(method, endpoint)
}

func (m *requestMetrics) observe(method, endpoint string, status int, seconds float64) {
	if endpoint == "/metrics" {
		return // Scraping must not generate application RED traffic.
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "CONNECT", "TRACE":
	default:
		method = "OTHER"
	}
	m.requests.WithLabelValues(method, endpoint, strconv.Itoa(status)).Inc()
	if status < 200 || status >= 300 {
		m.errors.WithLabelValues(method, endpoint).Inc()
	}
	m.duration.WithLabelValues(method, endpoint).Observe(seconds)
}

func (m *requestMetrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
