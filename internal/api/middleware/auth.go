package middleware

import (
	"errors"
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

// Auth returns middleware that verifies the request's JWT against the public key
// in its `sub` claim. Behavior depends on mode:
//
//   - Permissive: a request with no bearer token passes through anonymously; a
//     present-but-invalid token is rejected with 401.
//   - Required: any request without a valid token is rejected with 401.
//
// On success the authenticated user ID is attached to the request context
// (retrieve it with auth.UserIDFromContext). authMetrics may be nil.
func Auth(verifier auth.HTTPRequestVerifier, mode auth.Mode, authMetrics *metrics.Auth) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, err := verifier.VerifyHTTPRequest(r)
			switch {
			case err == nil:
				metrics.RecordAuth(authMetrics, "authenticated", "ok")
				r = r.WithContext(auth.ContextWithUserID(r.Context(), userID))

			case errors.Is(err, auth.ErrNoToken):
				if mode == auth.Required {
					metrics.RecordAuth(authMetrics, "rejected", "no_token")
					httperror.Unauthorized("unauthorized", nil).Render(w)
					return
				}
				// Permissive: allow through with no user ID attached.
				metrics.RecordAuth(authMetrics, "anonymous", "no_token")

			case errors.Is(err, auth.ErrUnauthorized):
				// A token was presented but did not verify — always rejected. The
				// reason drives both the metric label and a structured log line for
				// per-request diagnosis. err.Error() carries only the failure
				// category and request method/path, never the token or body bytes.
				reason := auth.Reason(err)
				metrics.RecordAuth(authMetrics, "rejected", reason)
				logger.InfoWithContext(r.Context(), "rejected request with invalid auth token",
					"reason", reason,
					"detail", err.Error(),
					"method", r.Method,
					"path", r.URL.Path)
				httperror.Unauthorized("unauthorized", nil).Render(w)
				return

			case IsMaxBytesError(err):
				// The request body exceeded the limit set by BodySizeLimit (which
				// runs upstream of this middleware), surfaced via the verifier's
				// io.ReadAll. This is a client-controlled condition, so render the
				// same 413 the body-reading handlers use, not a 500.
				metrics.RecordAuth(authMetrics, "rejected", "too_large")
				httperror.RequestEntityTooLarge("Request body too large", err).Render(w)
				return

			default:
				// Operational failure (e.g. reading the body).
				metrics.RecordAuth(authMetrics, "rejected", "internal")
				logger.ErrorWithContext(r.Context(), "auth check failed", "error", err)
				httperror.InternalServerError("An unexpected error occurred", err).Render(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
