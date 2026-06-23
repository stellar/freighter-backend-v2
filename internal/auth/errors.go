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
	Err       error
}

func (e *ExpiredTokenError) Error() string {
	return fmt.Sprintf("token expired by %s: %v", e.ExpiredBy, e.Err)
}

func (e *ExpiredTokenError) Unwrap() error { return e.Err }

// Is lets errors.Is(err, ErrUnauthorized) match an ExpiredTokenError.
func (e *ExpiredTokenError) Is(target error) bool { return target == ErrUnauthorized }

// Verification-failure reason labels. Bounded, low-cardinality, and safe for
// metrics/logging — they are fixed categories that never carry token, body, or
// request-value data.
const (
	ReasonExpired       = "expired"         // exp in the past (beyond leeway)
	ReasonBadSignature  = "bad_signature"   // signature/alg verification failed
	ReasonBadTiming     = "bad_timing"      // missing/inconsistent exp/iat, lifetime too long, exp too far future
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
	var ve *VerificationError
	if errors.As(err, &ve) {
		return ve.Reason
	}
	return "invalid"
}
