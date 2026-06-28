package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	jwtgo "github.com/golang-jwt/jwt/v5"
)

const (
	// MaxTokenLifetime caps how long a token may be valid (exp - iat). Short
	// lifetimes are the primary replay defense.
	MaxTokenLifetime = 15 * time.Second
	// ClockSkewLeeway tolerates clock drift between client and server when
	// checking expiry, per the design doc (mobile clients especially).
	ClockSkewLeeway = 5 * time.Second
)

// Claims is the JWT payload Freighter clients sign. Subject (the `sub` registered
// claim) is the hex-encoded Ed25519 auth public key, which doubles as the user ID
// and the signature verification key.
type Claims struct {
	BodyHash      string `json:"bodyHash"`
	MethodAndPath string `json:"methodAndPath"`
	jwtgo.RegisteredClaims
}

// Validate runs the non-cryptographic claim checks: timing bounds, and that the
// token is bound to this exact request (method+path and body). Signature and
// expiry-vs-now are verified separately by ParseJWT. Failures are returned as
// *VerificationError with a specific reason so the rejection can be classified
// for metrics/logging.
func (c *Claims) Validate(methodAndPath string, body []byte, maxLifetime time.Duration) error {
	if c.ExpiresAt == nil {
		return &VerificationError{Reason: ReasonBadTiming, Err: errors.New("missing exp claim")}
	}
	if c.IssuedAt == nil {
		return &VerificationError{Reason: ReasonBadTiming, Err: errors.New("missing iat claim")}
	}

	lifetime := c.ExpiresAt.Sub(c.IssuedAt.Time)
	if lifetime < 0 {
		return &VerificationError{Reason: ReasonBadTiming, Err: errors.New("exp precedes iat")}
	}
	if lifetime > maxLifetime {
		// Don't echo the configured maximum; keep the offending lifetime for
		// diagnostics but leave the server's threshold out of the detail.
		return &VerificationError{Reason: ReasonBadTiming, Err: fmt.Errorf("token lifetime %s exceeds maximum", lifetime)}
	}
	// Capture a single "now" so the iat/exp future bounds are checked against the
	// same instant.
	now := time.Now()
	// Reject a future-dated iat beyond the skew leeway. jwt/v5 with WithLeeway
	// validates exp/nbf but does not reject a future iat, so without this a signer
	// could date a token ahead of now (e.g. iat=exp=now+lifetime+leeway) and have
	// it accepted, stretching the acceptance window past the intended ±skew.
	if c.IssuedAt.After(now.Add(ClockSkewLeeway)) {
		return &VerificationError{Reason: ReasonBadTiming, Err: errors.New("iat is in the future beyond the allowed skew")}
	}
	// Reject tokens dated implausibly far in the future. exp can legitimately be
	// up to one full lifetime ahead, plus skew leeway.
	if c.ExpiresAt.After(now.Add(maxLifetime + ClockSkewLeeway)) {
		return &VerificationError{Reason: ReasonBadTiming, Err: errors.New("exp is too far in the future")}
	}

	if c.MethodAndPath != strings.TrimSpace(methodAndPath) {
		return &VerificationError{Reason: ReasonBadMethodPath, Err: fmt.Errorf("methodAndPath %q does not match expected %q", c.MethodAndPath, methodAndPath)}
	}
	if c.BodyHash != HashBody(body) {
		return &VerificationError{Reason: ReasonBadBodyHash, Err: errors.New("bodyHash does not match request body")}
	}

	return nil
}
