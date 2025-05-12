package middleware

import (
	"errors"
	"fmt"
	"net/http"

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
				w.WriteHeader(http.StatusInternalServerError)
				_, err = fmt.Fprintln(w, err)
				if err != nil {
					logger.ErrorWithContext(r.Context(), "Failed to write error to response",
						"error", err,
						"method", r.Method,
						"url", r.URL.String())
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
