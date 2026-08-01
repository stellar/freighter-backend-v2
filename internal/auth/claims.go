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
	// ClockSkewLeeway is the DEFAULT clock-drift tolerance between client and
	// server when checking iat/exp, per the design doc (mobile clients
	// especially). It is the default value of the --auth-clock-skew-leeway flag;
	// the effective leeway is threaded in per-verifier (see NewVerifier). This
	// const is read as that flag default and by the exported ParseJWT convenience
	// wrapper (parser.go); production request verification goes through the
	// verifier's configured leeway, not this const.
	//
	// Deliberately wide during the JWT rollout: the goal is to avoid rejecting
	// real users whose device clocks drift or are set ahead, while the
	// freighter_auth_requests_total{reason="bad_timing"|"expired"} counters
	// gather the real-world skew distribution. Tighten once that data is in.
	// Wider leeway proportionally widens the token replay window (~2*leeway +
	// MaxTokenLifetime); it never weakens signature verification.
	ClockSkewLeeway = 2 * time.Minute
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
func (c *Claims) Validate(methodAndPath string, body []byte, maxLifetime, leeway time.Duration) error {
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
	// These two are ReasonClockAhead, not ReasonBadTiming: the claims are
	// well-formed and self-consistent, they just describe a moment further ahead
	// than we allow — i.e. a client clock running fast, the mirror of an expired
	// token's lagging clock. The checks above stay ReasonBadTiming because they
	// indicate a malformed or abusive token rather than a wrong clock, and callers
	// (see middleware.Auth) treat the two very differently.
	//
	// AheadBy carries the overshoot so the magnitude is diagnosable from logs, the
	// same way ExpiredTokenError.ExpiredBy is for lagging clocks.
	if c.IssuedAt.After(now.Add(leeway)) {
		return &ClockAheadError{AheadBy: c.IssuedAt.Sub(now), Err: errors.New("iat is in the future beyond the allowed skew")}
	}
	// Reject tokens dated implausibly far in the future. exp can legitimately be
	// up to one full lifetime ahead, plus skew leeway.
	//
	// Defense in depth: this branch is UNREACHABLE given the checks above, and has
	// been since before ClockAheadError existed. They force
	//
	//	exp = iat + lifetime <= (now + leeway) + maxLifetime
	//
	// which is exactly the bound tested here, so the strict > can never hold.
	// (Verified by brute force over the iat/lifetime space, including an
	// exhaustive 1-second grid — zero reachable cases.) It is kept because it
	// stops being unreachable the moment either preceding check is reordered,
	// loosened, or removed, and an exp far in the future is precisely what the
	// lifetime cap exists to prevent.
	//
	// One consequence of that unreachability: AheadBy here would under-report by
	// (maxLifetime - lifetime) compared with the iat branch above, which reports
	// the clock offset directly. Both are "overshoot past the no-leeway bound", but
	// they only agree when lifetime == maxLifetime. Left as-is rather than
	// "corrected", since changing a value no caller can observe adds risk without
	// benefit — but if a reordering ever makes this reachable, fix the formula in
	// the same change.
	if c.ExpiresAt.After(now.Add(maxLifetime + leeway)) {
		return &ClockAheadError{AheadBy: c.ExpiresAt.Sub(now.Add(maxLifetime)), Err: errors.New("exp is too far in the future")}
	}

	if c.MethodAndPath != strings.TrimSpace(methodAndPath) {
		return &VerificationError{Reason: ReasonBadMethodPath, Err: fmt.Errorf("methodAndPath %q does not match expected %q", c.MethodAndPath, methodAndPath)}
	}
	if c.BodyHash != HashBody(body) {
		return &VerificationError{Reason: ReasonBadBodyHash, Err: errors.New("bodyHash does not match request body")}
	}

	return nil
}
