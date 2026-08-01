package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

// Bounds on client-controlled values written to rejection logs. Both the iss
// claim and the methodAndPath claim (echoed in a bad_method_path error's detail)
// are client-controlled and, on the rejection path, unverified; truncating caps
// log volume from an oversized claim. Metric labels are separately bounded by
// metrics.SanitizeClient.
const (
	maxLoggedIssuerLen = 64
	maxLoggedDetailLen = 256
)

// truncate returns s bounded to max bytes, cut on a valid UTF-8 boundary, with a
// marker when truncated.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.ToValidUTF8(s[:max], "") + "…(truncated)"
}

// truncateForLog bounds an iss value for logging.
func truncateForLog(s string) string { return truncate(s, maxLoggedIssuerLen) }

// Auth returns middleware that verifies the request's JWT against the public key
// in its `sub` claim. Behavior depends on mode:
//
//   - Permissive: a request with no bearer token passes through anonymously, as
//     does one whose token failed on CLOCK grounds — expired (clock lagging) or
//     clock_ahead (clock fast). Those are wrong-clock clients, not attackers, and
//     the gated routes already serve anonymous traffic. Any other invalid token
//     is rejected with 401, including bad_timing: that reason covers malformed
//     timing claims (missing exp/iat, exp before iat, over-long lifetime) rather
//     than a wrong clock. See the fall-through below for the full rationale.
//   - Required: any request without a valid token is rejected with 401, clock
//     failures included.
//
// On success the authenticated user ID is attached to the request context
// (retrieve it with auth.UserIDFromContext). authMetrics may be nil.
func Auth(verifier auth.HTTPRequestVerifier, mode auth.Mode, authMetrics *metrics.Auth) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, err := verifier.VerifyHTTPRequest(r)
			switch {
			case err == nil:
				metrics.RecordAuth(authMetrics, "authenticated", "ok", metrics.SanitizeClient(identity.Issuer))
				f := logger.FieldsFromContext(r.Context())
				f.Set("user_id", identity.UserID)
				f.Set("iss", truncateForLog(identity.Issuer))
				r = r.WithContext(auth.ContextWithUserID(r.Context(), identity.UserID))

			case errors.Is(err, auth.ErrNoToken):
				if mode == auth.Required {
					metrics.RecordAuth(authMetrics, "rejected", "no_token", metrics.ClientNone)
					httperror.Unauthorized("unauthorized", nil).Render(w)
					return
				}
				// Permissive: allow through with no user ID attached.
				metrics.RecordAuth(authMetrics, "anonymous", "no_token", metrics.ClientNone)

			case errors.Is(err, auth.ErrUnauthorized):
				// A token was presented but did not verify. The reason drives both the
				// metric label and a structured log line for per-request diagnosis.
				// err.Error() never contains the token or body bytes, but a
				// bad_method_path error echoes the client-controlled methodAndPath
				// claim, so the detail is length-bounded before logging.
				reason := auth.Reason(err)
				iss := auth.IssuerFromRequestUnverified(r)

				// In permissive mode a wrong client clock — in EITHER direction — is
				// served anonymously instead of rejected. The routes behind this
				// middleware already serve anonymous traffic in permissive, so 401ing a
				// client whose only fault is a bad device clock refuses a request that
				// would have succeeded had the client sent no token at all, which
				// defeats the point of permissive mode and locks the user out of every
				// gated route.
				//
				// Both clock directions, deliberately symmetric:
				//   ReasonExpired    — clock lagging (exp already past)
				//   ReasonClockAhead — clock fast    (iat/exp too far ahead)
				//
				// ReasonBadTiming is NOT permitted. It is what remains of the old
				// catch-all timing bucket after the clock cases were split out of it:
				// missing exp/iat, exp preceding iat, and over-long lifetimes. Those
				// are malformed or abusive tokens, not wrong clocks, and no real client
				// produces them — serving them would be indistinguishable in the
				// metrics from serving genuine skew, in the exact counter that gates
				// the permissive→strict flip.
				//
				// On signature verification: ReasonExpired implies the signature
				// verified (it comes from jwtgo.ParseWithClaims, and jwt/v5 returns on
				// signature failure before validating claims). ReasonClockAhead does
				// NOT — Claims.Validate runs first, so a forged token dated into the
				// future lands here without ever reaching the signature check. That is
				// safe rather than merely tolerable: permitting attaches no userID, so
				// the request is byte-for-byte equivalent to one carrying no
				// Authorization header, which these routes already serve. An attacker
				// gains nothing they could not have by sending no token at all. What it
				// does mean is that invalid_permitted{reason="clock_ahead"} is a noisier
				// signal than the expired one — read it as an upper bound on fast-clock
				// clients, not an exact count.
				//
				// Strict stays fail-closed for everything: it has no anonymous path to
				// fall back to, so skewed clients must be fixed client-side before the
				// permissive→strict flip.
				permitted := mode == auth.Permissive &&
					(reason == auth.ReasonExpired || reason == auth.ReasonClockAhead)

				result := "rejected"
				if permitted {
					result = metrics.ResultInvalidPermitted
				}
				metrics.RecordAuth(authMetrics, result, reason, metrics.SanitizeClient(iss))

				loggedIss := truncateForLog(iss)
				logger.FieldsFromContext(r.Context()).Set("iss", loggedIss)
				logger.InfoWithContext(r.Context(), "invalid auth token",
					"reason", reason,
					"permitted", permitted,
					"iss", loggedIss,
					"detail", truncate(err.Error(), maxLoggedDetailLen),
					"method", r.Method,
					"path", r.URL.Path)

				if permitted {
					// Fall through to next.ServeHTTP below — anonymous, no userID.
					break
				}
				httperror.Unauthorized("unauthorized", nil).Render(w)
				return

			case IsMaxBytesError(err):
				// The request body exceeded the limit set by BodySizeLimit (which
				// runs upstream of this middleware), surfaced via the verifier's
				// io.ReadAll. This is a client-controlled condition, so render the
				// same 413 the body-reading handlers use, not a 500.
				iss := auth.IssuerFromRequestUnverified(r)
				metrics.RecordAuth(authMetrics, "rejected", "too_large", metrics.SanitizeClient(iss))
				logger.FieldsFromContext(r.Context()).Set("iss", truncateForLog(iss))
				httperror.RequestEntityTooLarge("Request body too large", err).Render(w)
				return

			default:
				// Operational failure (e.g. reading the body).
				iss := auth.IssuerFromRequestUnverified(r)
				metrics.RecordAuth(authMetrics, "rejected", "internal", metrics.SanitizeClient(iss))
				logger.FieldsFromContext(r.Context()).Set("iss", truncateForLog(iss))
				logger.ErrorWithContext(r.Context(), "auth check failed", "error", err)
				httperror.InternalServerError("An unexpected error occurred", err).Render(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
