package auth

import (
	"errors"
	"fmt"
	"time"
)

// ErrNoToken signals that a request carried no bearer token. It is deliberately
// NOT wrapped under ErrUnauthorized: the middleware distinguishes "no token"
// (which may be allowed through in permissive mode) from a present-but-invalid
// token (always rejected).
var ErrNoToken = errors.New("no authorization token provided")

// ErrUnauthorized is the sentinel that all token-validation failures wrap. The
// middleware renders any error matching it as a 401.
var ErrUnauthorized = errors.New("not authorized")

// ExpiredTokenError marks a token that failed verification solely because it
// expired (beyond the clock-skew leeway). It wraps ErrUnauthorized so callers
// that don't care about the distinction still treat it as a 401.
type ExpiredTokenError struct {
	ExpiredBy time.Duration
	// Issuer is the token's `iss` claim, and unlike the value
	// IssuerFromRequestUnverified recovers it is SIGNATURE-VERIFIED: this error is
	// only constructed after jwtgo.ParseWithClaims has checked the signature
	// (jwt/v5 returns ErrTokenSignatureInvalid before it validates claims, so
	// reaching ErrTokenExpired implies the signature passed). It is carried out of
	// the failure for the same reason ExpiredBy is — so the caller gets an
	// authentic observability label instead of re-parsing the token unverified.
	//
	// ClockAheadError has no counterpart yet — but no longer for the original
	// reason. It used to be raised before any signature check, so no authentic
	// issuer existed to carry; now that the signature is the first gate it is
	// raised only after verification, so one does exist. Until it carries it, the
	// middleware falls back to the spoofable header value for clock_ahead.
	Issuer string
	Err    error
}

func (e *ExpiredTokenError) Error() string {
	return fmt.Sprintf("token expired by %s: %v", e.ExpiredBy, e.Err)
}

func (e *ExpiredTokenError) Unwrap() error { return e.Err }

// Is lets errors.Is(err, ErrUnauthorized) match an ExpiredTokenError.
func (e *ExpiredTokenError) Is(target error) bool { return target == ErrUnauthorized }

// ClockAheadError marks a token whose claims are well-formed but dated further
// into the future than the clock-skew leeway allows — the mirror of
// ExpiredTokenError. It wraps ErrUnauthorized so callers that don't care about
// the distinction still treat it as a 401.
//
// Like ExpiredTokenError, it is only ever raised after the signature has been
// verified (parseJWT checks the signature before validating claims), so it does
// imply the token was signed by sub. That is what makes it safe for
// middleware.Auth to permit in permissive mode.
type ClockAheadError struct {
	AheadBy time.Duration
	Err     error
}

func (e *ClockAheadError) Error() string {
	return fmt.Sprintf("token clock ahead by %s: %v", e.AheadBy, e.Err)
}

func (e *ClockAheadError) Unwrap() error { return e.Err }

// Is lets errors.Is(err, ErrUnauthorized) match a ClockAheadError.
func (e *ClockAheadError) Is(target error) bool { return target == ErrUnauthorized }

// Verification-failure reason labels. Bounded, low-cardinality, and safe for
// metrics/logging — they are fixed categories that never carry token, body, or
// request-value data.
const (
	ReasonExpired      = "expired"       // exp in the past (beyond leeway) — a LAGGING client clock
	ReasonBadSignature = "bad_signature" // signature/alg verification failed
	// ReasonClockAhead is a client clock running FAST: iat, exp, or nbf is further
	// in the future than the leeway allows. It is the mirror of ReasonExpired,
	// split out of ReasonBadTiming so a wrong clock is distinguishable from a
	// malformed token — both are "timing" failures, but only this one is a clock
	// symptom, and conflating them makes the permitted-skew rate unreadable.
	//
	// Like ReasonExpired, it is assigned only after the signature verifies (see
	// parser.go), so it is evidence of a real key holder whose clock is wrong —
	// not something an unauthenticated caller can manufacture.
	ReasonClockAhead    = "clock_ahead"
	ReasonBadTiming     = "bad_timing"      // missing/inconsistent exp/iat, or lifetime too long
	ReasonBadMethodPath = "bad_method_path" // methodAndPath claim does not match the request
	ReasonBadBodyHash   = "bad_body_hash"   // bodyHash claim does not match the request body
	ReasonBadSubject    = "bad_subject"     // subject is not a valid hex Ed25519 public key
	ReasonMalformed     = "malformed"       // token could not be parsed at all
)

// VerificationError categorizes a non-expiry token-verification failure so the
// reason can be surfaced as a bounded metric/log label. It wraps ErrUnauthorized
// so all token failures render as 401.
type VerificationError struct {
	Reason string
	Err    error
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("jwt verification failed (%s): %v", e.Reason, e.Err)
}

func (e *VerificationError) Unwrap() error { return e.Err }

// Is lets errors.Is(err, ErrUnauthorized) match a VerificationError.
func (e *VerificationError) Is(target error) bool { return target == ErrUnauthorized }

// Reason returns a low-cardinality classification of a verification failure for
// metrics and logging. It returns "invalid" for an unrecognized non-nil error.
func Reason(err error) string {
	var expired *ExpiredTokenError
	if errors.As(err, &expired) {
		return ReasonExpired
	}
	var ahead *ClockAheadError
	if errors.As(err, &ahead) {
		return ReasonClockAhead
	}
	var ve *VerificationError
	if errors.As(err, &ve) {
		return ve.Reason
	}
	return "invalid"
}
