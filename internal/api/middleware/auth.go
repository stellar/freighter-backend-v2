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

				// Discriminate on the error TYPES, not on the flattened reason
				// string. Both clock failures have dedicated types, and the string
				// is a plain field set at ten *VerificationError sites across
				// internal/auth — so a string predicate makes the fail-open set
				// "anything whose reason matches", extensible from another file
				// without touching this one. This exact mechanism already failed
				// here once: 5b32ac7 permitted ReasonBadTiming and served tokens
				// with no exp claim at all, caught by review rather than by a test.
				// Requiring a new error type keeps extension visible in the diff.
				//
				// reason is still used below for the metric label and log field.
				var expiredErr *auth.ExpiredTokenError
				var clockAheadErr *auth.ClockAheadError
				isExpired := errors.As(err, &expiredErr)
				isClockAhead := errors.As(err, &clockAheadErr)

				// Prefer the signature-verified issuer when one exists. The
				// unverified fallback is client-spoofable, and the permitted
				// fall-through below puts this label on the SERVING path, where
				// IssuerFromRequestUnverified's own contract says not to rely on it.
				//
				// BOTH permitted reasons carry an authentic iss, for the same reason
				// they are permitted at all: neither is reachable without the private
				// key for sub, since the signature is verified before any claim is
				// inspected. So every request that reaches the serving path below is
				// attributed to a client bucket the sender cannot choose — which is
				// what makes the per-iss split trustworthy as the signal for which
				// client team still needs to ship the Date-header offset fix.
				//
				// The unverified fallback remains only for the rejection path, where
				// no verified value exists (bad_signature, malformed, bad_subject).
				iss := auth.IssuerFromRequestUnverified(r)
				issVerified := false
				switch {
				case isExpired && expiredErr.Issuer != "":
					iss = expiredErr.Issuer
					issVerified = true
				case isClockAhead && clockAheadErr.Issuer != "":
					iss = clockAheadErr.Issuer
					issVerified = true
				}

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
				// Both permitted reasons are signature-backed. parseJWT verifies the
				// signature BEFORE validating claims, and jwt/v5 likewise returns on
				// signature failure before running its own claim checks, so neither
				// ReasonExpired nor ReasonClockAhead is reachable without the private
				// key for sub. That is what makes permitting them safe to act on and
				// makes invalid_permitted an honest count of real clients with wrong
				// clocks — an unauthenticated caller cannot manufacture either reason,
				// so it cannot hold the permissive→strict decision open by flooding the
				// counter it is read from.
				//
				// Keep it that way: if a claims check is ever moved back ahead of the
				// signature gate, whatever reason it produces stops being evidence of a
				// real user and must not appear in this predicate.
				//
				// Strict stays fail-closed for everything: it has no anonymous path to
				// fall back to, so skewed clients must be fixed client-side before the
				// permissive→strict flip.
				permitted := mode == auth.Permissive && (isExpired || isClockAhead)

				result := "rejected"
				if permitted {
					result = metrics.ResultInvalidPermitted
				}
				metrics.RecordAuth(authMetrics, result, reason, metrics.SanitizeClient(iss))

				loggedIss := truncateForLog(iss)
				logger.FieldsFromContext(r.Context()).Set("iss", loggedIss)
				// iss_verified makes the trust level of iss self-describing. Without
				// it, a permitted request logs a spoofable iss alongside a 200, and
				// the only tell is the ABSENCE of user_id — so any aggregation by iss
				// over 200s silently mixes verified and unverified issuers. Kept as a
				// boolean beside iss rather than a renamed key so existing iss
				// queries keep working and can filter on it.
				logger.FieldsFromContext(r.Context()).Set("iss_verified", issVerified)
				logger.InfoWithContext(r.Context(), "invalid auth token",
					"reason", reason,
					"permitted", permitted,
					"iss", loggedIss,
					"iss_verified", issVerified,
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
