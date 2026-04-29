// ABOUTME: HTTP middleware that records Prometheus metrics for each request.
// ABOUTME: Reads final status from BufferedResponseWriter after handler completes.
package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

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

			labels := []string{handler, r.Method, strconv.Itoa(code)}
			h.RequestsTotal.WithLabelValues(labels...).Inc()
			h.RequestDuration.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
		})
	}
}
