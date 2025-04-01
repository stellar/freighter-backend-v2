package middleware

import (
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/logger"
)

// Logging returns a middleware that logs information about each request
func LoggingMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startTime := time.Now()
			next.ServeHTTP(w, r)
			duration := time.Since(startTime)
			logger.Info("Request completed",
				"method", r.Method,
				"url", r.URL.String(),
				"duration", duration)
		})
	}
}
