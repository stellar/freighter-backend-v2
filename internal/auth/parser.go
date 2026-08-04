package auth

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	jwtgo "github.com/golang-jwt/jwt/v5"
)

// ParseJWT verifies a Freighter auth token against the self-asserted public key
// in its `sub` claim and the bindings for this request (methodAndPath, body).
//
// Unlike wallet-backend's JWTManager, there is no pre-configured/allowlisted
// server key: the verification key is derived from the token's own subject, so
// the token proves "whoever signed this controls the private key for pubkey
// <sub>" — which is exactly the user identity. All validation failures wrap
// ErrUnauthorized; an expired token additionally surfaces as *ExpiredTokenError.
func ParseJWT(tokenString, methodAndPath string, body []byte) (*Claims, error) {
	return parseJWT(tokenString, methodAndPath, body, ClockSkewLeeway)
}

// parseJWT is ParseJWT with an explicit clock-skew leeway, threaded in from the
// verifier so it can be configured per deployment (--auth-clock-skew-leeway).
// Widening the leeway only widens which iat/exp values are tolerated; the
// signature is verified first and unconditionally, so a forged-signature token is
// rejected as bad_signature regardless of leeway and regardless of its claims.
//
// Order matters and is load-bearing: subject -> signature -> claims. Every reason
// this returns after the signature gate implies the token was signed by sub.
// Callers rely on that (see middleware.Auth, which permits some reasons in
// permissive mode and reports all of them as metrics that gate the rollout).
func parseJWT(tokenString, methodAndPath string, body []byte, leeway time.Duration) (*Claims, error) {
	claims := &Claims{}

	// Read the claims without verifying the signature, solely to learn the subject
	// — the key we must verify against. Nothing is trusted from this parse.
	if _, _, err := jwtgo.NewParser().ParseUnverified(tokenString, claims); err != nil {
		return nil, &VerificationError{Reason: ReasonMalformed, Err: err}
	}

	pubKey, err := decodePublicKey(claims.Subject)
	if err != nil {
		return nil, &VerificationError{Reason: ReasonBadSubject, Err: err}
	}

	// The signature is the FIRST gate. Every failure reason produced below it is
	// therefore a statement about a token that was actually signed by sub, which
	// is what makes those reasons safe to act on — middleware.Auth permits some of
	// them in permissive mode, and the metrics they feed gate the permissive→strict
	// flip. A reason assignable without the private key would let anyone drive
	// both. jwt/v5 verifies the signature before validating claims, so its own
	// claim errors below carry the same guarantee.
	//
	// The cost is one Ed25519 verify (~50µs) on tokens that a claims check would
	// have rejected more cheaply. That is deliberate: cheap-checks-first is what
	// made the reasons unauthenticated.
	_, err = jwtgo.ParseWithClaims(tokenString, claims,
		func(*jwtgo.Token) (any, error) { return pubKey, nil },
		// Pin the algorithm to EdDSA to prevent alg-confusion (e.g. alg=none or
		// an HS256 token forged with the public key as the HMAC secret).
		jwtgo.WithValidMethods([]string{"EdDSA"}),
		jwtgo.WithLeeway(leeway),
	)
	if err != nil {
		switch {
		case errors.Is(err, jwtgo.ErrTokenExpired):
			// Lagging client clock.
			var expiredBy time.Duration
			if claims.ExpiresAt != nil {
				expiredBy = time.Since(claims.ExpiresAt.Time)
			}
			// claims.Issuer is authentic here: ParseWithClaims verified the
			// signature before validating claims, so an expiry error implies a
			// genuine holder of the subject's private key.
			return nil, &ExpiredTokenError{ExpiredBy: expiredBy, Issuer: claims.Issuer, Err: err}
		case errors.Is(err, jwtgo.ErrTokenNotValidYet):
			// Fast client clock: nbf is further ahead than the leeway allows. This
			// is the same symptom as a future iat, so it gets the same reason —
			// classifying it as a signature failure would report a wrong clock as a
			// forgery and 401 the user even in permissive mode.
			var aheadBy time.Duration
			if claims.NotBefore != nil {
				aheadBy = time.Until(claims.NotBefore.Time)
			}
			return nil, &ClockAheadError{AheadBy: aheadBy, Err: err}
		}
		// Signature/algorithm failure (wrong key, tampered, alg confusion).
		return nil, &VerificationError{Reason: ReasonBadSignature, Err: err}
	}

	// Non-cryptographic checks: timing bounds and request binding. Reached only
	// with a verified signature, per the gate above. Validate already returns a
	// *VerificationError (or *ClockAheadError) with a specific reason.
	if err := claims.Validate(methodAndPath, body, MaxTokenLifetime, leeway); err != nil {
		return nil, err
	}

	// Canonicalize the subject before exposing it as the user ID. hex.DecodeString
	// accepts upper/mixed-case, so the same key could otherwise arrive as distinct
	// strings; re-encoding the decoded bytes yields the canonical lowercase form so
	// callers that key storage/cache by the user ID never split one key into two.
	claims.Subject = hex.EncodeToString(pubKey)

	return claims, nil
}

// decodePublicKey turns the hex-encoded `sub` claim into an Ed25519 public key.
func decodePublicKey(sub string) (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(sub)
	if err != nil {
		return nil, fmt.Errorf("subject is not valid hex: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("subject decodes to %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
