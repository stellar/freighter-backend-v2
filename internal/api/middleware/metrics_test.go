// ABOUTME: Unit tests for the Prometheus metrics middleware.
// ABOUTME: Verifies request counting, duration recording, in-flight gauge, and unknown handler label.
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

func newTestHTTPMetrics(t *testing.T) (*metrics.HTTP, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	h := metrics.NewHTTP(reg)
	return h, reg
}

func TestMetricsMiddleware_RecordsRequestCount(t *testing.T) {
	h, _ := newTestHTTPMetrics(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := Metrics(h)(inner)

	// Use a ServeMux to populate r.Pattern
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/ping", mw)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	count := testutil.ToFloat64(h.RequestsTotal.WithLabelValues("GET /api/v1/ping", "GET", "200"))
	assert.Equal(t, float64(1), count)
}

func TestMetricsMiddleware_RecordsDuration(t *testing.T) {
	h, reg := newTestHTTPMetrics(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := Metrics(h)(inner)

	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/ping", mw)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	count := testutil.CollectAndCount(reg, "freighter_http_request_duration_seconds")
	assert.Equal(t, 1, count)
}

func TestMetricsMiddleware_InFlightGauge(t *testing.T) {
	h, _ := newTestHTTPMetrics(t)

	var inFlightDuring float64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlightDuring = testutil.ToFloat64(h.InFlightRequests)
		w.WriteHeader(http.StatusOK)
	})

	mw := Metrics(h)(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, float64(1), inFlightDuring)
	assert.Equal(t, float64(0), testutil.ToFloat64(h.InFlightRequests))
}

func TestMetricsMiddleware_UnknownHandler(t *testing.T) {
	h, _ := newTestHTTPMetrics(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	mw := Metrics(h)(inner)

	// Wrap in BufferedResponseWriter like Logging middleware does in production
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := NewBufferedResponseWriter(w)
		mw.ServeHTTP(bw, r)
	})

	// Direct call without ServeMux, so r.Pattern is empty
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	count := testutil.ToFloat64(h.RequestsTotal.WithLabelValues("unknown", "GET", "404"))
	assert.Equal(t, float64(1), count)
}

func TestMetricsMiddleware_ReadsBufferedResponseWriter(t *testing.T) {
	h, _ := newTestHTTPMetrics(t)

	// Simulate the Logging middleware creating a BufferedResponseWriter
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	// Wrap with a handler that creates a BufferedResponseWriter (like Logging does)
	bufferingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := NewBufferedResponseWriter(w)
		metricsHandler := Metrics(h)(inner)
		metricsHandler.ServeHTTP(bw, r)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	rec := httptest.NewRecorder()
	bufferingHandler.ServeHTTP(rec, req)

	count := testutil.ToFloat64(h.RequestsTotal.WithLabelValues("unknown", "GET", "400"))
	assert.Equal(t, float64(1), count)
}

func TestMetricsMiddleware_ErrorStatusCode(t *testing.T) {
	h, _ := newTestHTTPMetrics(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	mw := Metrics(h)(inner)

	// Wrap with BufferedResponseWriter like Logging middleware does in production
	bufferingMW := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := NewBufferedResponseWriter(w)
		mw.ServeHTTP(bw, r)
	})

	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/collectibles", bufferingMW)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/collectibles", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	count := testutil.ToFloat64(h.RequestsTotal.WithLabelValues("POST /api/v1/collectibles", "POST", "500"))
	assert.Equal(t, float64(1), count)
}

func TestMetricsMiddleware_BucketsNonStandardMethod(t *testing.T) {
	h, reg := newTestHTTPMetrics(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	mw := Metrics(h)(inner)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := NewBufferedResponseWriter(w)
		mw.ServeHTTP(bw, r)
	})

	// Send several unique, non-standard method tokens against an unmatched
	// route. Each must collapse to the single "other" bucket so an attacker
	// cannot grow label cardinality without bound.
	for _, method := range []string{"ATTACK1", "ATTACK2", "ATTACK3"} {
		req := httptest.NewRequest(method, "/nonexistent", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// Exactly one series exists, under the "other" bucket, with all 3 requests.
	count := testutil.ToFloat64(h.RequestsTotal.WithLabelValues("unknown", "other", "404"))
	assert.Equal(t, float64(3), count)
	assert.Equal(t, 1, testutil.CollectAndCount(reg, "freighter_http_requests_total"))
}

func TestMetricsMiddleware_MultipleRequests(t *testing.T) {
	h, _ := newTestHTTPMetrics(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := Metrics(h)(inner)

	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/ping", mw)

	for range 5 {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
	}

	count := testutil.ToFloat64(h.RequestsTotal.WithLabelValues("GET /api/v1/ping", "GET", "200"))
	require.Equal(t, float64(5), count)
}
