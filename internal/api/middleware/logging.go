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
			body := bw.Body()

			// withCommon prefixes the fields shared by every request log line and
			// appends any request-scoped fields (e.g. user_id/iss) set by downstream
			// middleware, so each branch specifies only what is unique to it. The
			// request-scoped fields always come last, preserving the existing key
			// order (log continuity).
			withCommon := func(extra ...any) []any {
				common := []any{
					"status", status,
					"method", r.Method,
					"url", r.URL.String(),
					"duration", duration,
				}
				return append(append(common, extra...), fields.Args()...)
			}

			switch {
			case err != nil:
				logger.ErrorWithContext(r.Context(), "Request failed to flush response",
					withCommon("error", err, "bodySize", len(body))...)
			case status >= 400:
				bodyStr := string(body)
				if len(bodyStr) > 1024 {
					bodyStr = bodyStr[:1024] + "... (truncated)"
				}
				logger.ErrorWithContext(r.Context(), "Request completed with error",
					withCommon("bodySize", len(body), "body", bodyStr)...)
			default:
				logger.InfoWithContext(r.Context(), "Request completed",
					withCommon("bodySize", len(body))...)
			}
		})
	}
}
