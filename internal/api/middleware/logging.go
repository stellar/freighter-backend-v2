package middleware

import (
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/logger"
)

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Logging returns a middleware that logs information about each request
func Logging() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startTime := time.Now()

			rw := &responseWriter{
				ResponseWriter: w,
			}
			next.ServeHTTP(rw, r)
			duration := time.Since(startTime)
			logger.Info("Request completed",
				"status", rw.status,
				"method", r.Method,
				"url", r.URL.String(),
				"duration", duration)
		})
	}
}
