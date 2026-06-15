// ABOUTME: HTTP middleware that records Prometheus metrics for each request.
// ABOUTME: Reads final status from BufferedResponseWriter after handler completes.
package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

// sanitizeMethod buckets any non-standard HTTP method into a single "other"
// label. Go's HTTP server accepts arbitrary method tokens from the client, and
// the method flows into Prometheus metric vectors (which retain a time series
// per unique label value). Without bucketing, a caller could send unique
// method tokens — especially against unmatched routes, where the handler label
// collapses to "unknown" — and grow label cardinality (and memory) without
// bound.
func sanitizeMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodConnect,
		http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "other"
	}
}

// Metrics returns middleware that records HTTP metrics using BufferedResponseWriter.
func Metrics(h *metrics.HTTP) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.InFlightRequests.Inc()
			defer h.InFlightRequests.Dec()

			start := time.Now()
			next.ServeHTTP(w, r)

			// Read final status from BufferedResponseWriter
			code := http.StatusOK
			if bw, ok := w.(*BufferedResponseWriter); ok {
				code = bw.StatusCode()
			}

			handler := r.Pattern
			if handler == "" {
				handler = "unknown"
			}

			labels := []string{handler, sanitizeMethod(r.Method), strconv.Itoa(code)}
			h.RequestsTotal.WithLabelValues(labels...).Inc()
			h.RequestDuration.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
		})
	}
}
