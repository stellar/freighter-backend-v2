package middleware

import (
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/logger"
)

// Logging returns a middleware that logs information about each request
// It uses a buffered response writer to allow handlers to change status codes
// even after writing the response body
func Logging() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startTime := time.Now()

			bw := NewBufferedResponseWriter(w)
			next.ServeHTTP(bw, r)
			err := bw.Flush()

			duration := time.Since(startTime)
			status := bw.StatusCode()

			if err != nil {
				logger.ErrorWithContext(r.Context(), "Request failed to flush response",
					"status", status,
					"method", r.Method,
					"url", r.URL.String(),
					"duration", duration,
					"error", err,
					"bodySize", len(bw.Body()))
			} else if status >= 400 {
				logger.ErrorWithContext(r.Context(), "Request completed with error",
					"status", status,
					"method", r.Method,
					"url", r.URL.String(),
					"duration", duration,
					"bodySize", len(bw.Body()))
			} else {
				logger.InfoWithContext(r.Context(), "Request completed",
					"status", status,
					"method", r.Method,
					"url", r.URL.String(),
					"duration", duration,
					"bodySize", len(bw.Body()))
			}
		})
	}
}
