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
	claims := &Claims{}

	// Read the claims without verifying the signature so we can learn the
	// subject (= the key we must verify against) and run cheap checks first.
	if _, _, err := jwtgo.NewParser().ParseUnverified(tokenString, claims); err != nil {
		return nil, &VerificationError{Reason: ReasonMalformed, Err: err}
	}

	// Validate already returns a *VerificationError with a specific reason.
	if err := claims.Validate(methodAndPath, body, MaxTokenLifetime); err != nil {
		return nil, err
	}

	pubKey, err := decodePublicKey(claims.Subject)
	if err != nil {
		return nil, &VerificationError{Reason: ReasonBadSubject, Err: err}
	}

	_, err = jwtgo.ParseWithClaims(tokenString, claims,
		func(*jwtgo.Token) (any, error) { return pubKey, nil },
		// Pin the algorithm to EdDSA to prevent alg-confusion (e.g. alg=none or
		// an HS256 token forged with the public key as the HMAC secret).
		jwtgo.WithValidMethods([]string{"EdDSA"}),
		jwtgo.WithLeeway(ClockSkewLeeway),
	)
	if err != nil {
		if errors.Is(err, jwtgo.ErrTokenExpired) {
			var expiredBy time.Duration
			if claims.ExpiresAt != nil {
				expiredBy = time.Since(claims.ExpiresAt.Time)
			}
			return nil, &ExpiredTokenError{ExpiredBy: expiredBy, Err: err}
		}
		// Signature/algorithm failure (wrong key, tampered, alg confusion).
		return nil, &VerificationError{Reason: ReasonBadSignature, Err: err}
	}

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
