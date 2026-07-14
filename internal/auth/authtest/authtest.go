// Package authtest provides shared helpers for minting Ed25519-signed API tokens
// in tests. The minting logic lives here, in one place, so the api and middleware
// test packages can't drift out of sync if the claims format ever changes.
package authtest

import (
	"crypto/ed25519"
	"testing"
	"time"

	jwtgo "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/auth"
)

// MintToken mints a valid Ed25519 JWT bound to sub and methodAndPath, expiring
// lifetime after issuedAt. Callers vary lifetime/issuedAt to exercise the expiry
// and clock-skew paths. The body hash is always the empty-body hash, matching the
// GET requests these tests issue.
func MintToken(t testing.TB, priv ed25519.PrivateKey, sub, methodAndPath string, lifetime time.Duration, issuedAt time.Time) string {
	t.Helper()
	c := auth.Claims{
		BodyHash:      auth.HashBody(nil),
		MethodAndPath: methodAndPath,
		RegisteredClaims: jwtgo.RegisteredClaims{
			Subject:   sub,
			Issuer:    "freighter-extension",
			IssuedAt:  jwtgo.NewNumericDate(issuedAt),
			ExpiresAt: jwtgo.NewNumericDate(issuedAt.Add(lifetime)),
		},
	}
	s, err := jwtgo.NewWithClaims(jwtgo.SigningMethodEdDSA, c).SignedString(priv)
	require.NoError(t, err)
	return s
}
