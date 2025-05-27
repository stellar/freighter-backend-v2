package middleware

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/logger"
)

func Recover() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}

				err, ok := rec.(error)
				if !ok {
					err = fmt.Errorf("panic: %v", rec)
				}

				// No need to recover when the client has disconnected:
				if errors.Is(err, http.ErrAbortHandler) {
					panic(err)
				}

				logger.ErrorWithContext(r.Context(), "Request panicked",
					"status", http.StatusInternalServerError,
					"error", err,
					"method", r.Method,
					"url", r.URL.String())

				// Check if we're using a buffered response writer
				if bw, ok := w.(*BufferedResponseWriter); ok {
					// Reset the buffer to clear any partial response
					bw.Reset()
				}

				// Create a proper error response
				httpErr := httperror.InternalServerError("An unexpected error occurred", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(httpErr.StatusCode)

				if err := json.NewEncoder(w).Encode(httpErr); err != nil {
					logger.ErrorWithContext(r.Context(), "Failed to write error response",
						"error", err,
						"method", r.Method,
						"url", r.URL.String())
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
