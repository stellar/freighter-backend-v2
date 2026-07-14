package middleware

import (
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/logger"
)

// Logging returns a middleware that logs one structured line per request.
// It seeds a request-scoped logger.Fields holder into the context so downstream
// middleware (e.g. Auth) can enrich the line with fields like user_id/iss; those
// fields are appended to the existing keys, never replacing them (log continuity).
func Logging() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startTime := time.Now()

			fields := logger.NewFields()
			r = r.WithContext(logger.ContextWithFields(r.Context(), fields))

			bw := NewBufferedResponseWriter(w)
			next.ServeHTTP(bw, r)
			err := bw.Flush()

			duration := time.Since(startTime)
			status := bw.StatusCode()

			if err != nil {
				logger.ErrorWithContext(r.Context(), "Request failed to flush response",
					append([]any{
						"status", status,
						"method", r.Method,
						"url", r.URL.String(),
						"duration", duration,
						"error", err,
						"bodySize", len(bw.Body()),
					}, fields.Args()...)...)
			} else if status >= 400 {
				body := bw.Body()
				bodyStr := string(body)
				if len(bodyStr) > 1024 {
					bodyStr = bodyStr[:1024] + "... (truncated)"
				}
				logger.ErrorWithContext(r.Context(), "Request completed with error",
					append([]any{
						"status", status,
						"method", r.Method,
						"url", r.URL.String(),
						"duration", duration,
						"bodySize", len(body),
						"body", bodyStr,
					}, fields.Args()...)...)
			} else {
				logger.InfoWithContext(r.Context(), "Request completed",
					append([]any{
						"status", status,
						"method", r.Method,
						"url", r.URL.String(),
						"duration", duration,
						"bodySize", len(bw.Body()),
					}, fields.Args()...)...)
			}
		})
	}
}
