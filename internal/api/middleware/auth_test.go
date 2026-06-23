package middleware

import (
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwtgo "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/auth"
)

const authTestPath = "/api/v1/auth/whoami"

func mintToken(t *testing.T, priv ed25519.PrivateKey, sub, methodAndPath string, lifetime time.Duration, issuedAt time.Time) string {
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

func TestAuth_TruthTable(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	_, otherPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	methodAndPath := "GET " + authTestPath
	now := time.Now()

	validToken := func() string { return mintToken(t, priv, sub, methodAndPath, auth.MaxTokenLifetime, now) }
	expiredToken := func() string {
		return mintToken(t, priv, sub, methodAndPath, auth.MaxTokenLifetime, now.Add(-1*time.Minute))
	}
	// Signed by a different key than the one declared in sub.
	wrongKeyToken := func() string { return mintToken(t, otherPriv, sub, methodAndPath, auth.MaxTokenLifetime, now) }

	cases := []struct {
		name         string
		mode         auth.Mode
		bearer       string
		wantStatus   int
		wantUserID   bool
		wantUserIDeq string
	}{
		{"permissive/no-header", auth.Permissive, "", http.StatusOK, false, ""},
		{"permissive/valid", auth.Permissive, validToken(), http.StatusOK, true, sub},
		{"permissive/expired", auth.Permissive, expiredToken(), http.StatusUnauthorized, false, ""},
		{"permissive/wrong-key", auth.Permissive, wrongKeyToken(), http.StatusUnauthorized, false, ""},
		{"required/no-header", auth.Required, "", http.StatusUnauthorized, false, ""},
		{"required/valid", auth.Required, validToken(), http.StatusOK, true, sub},
		{"required/expired", auth.Required, expiredToken(), http.StatusUnauthorized, false, ""},
		{"required/wrong-key", auth.Required, wrongKeyToken(), http.StatusUnauthorized, false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				reached    bool
				gotUserID  string
				gotHasUser bool
			)
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				gotUserID, gotHasUser = auth.UserIDFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			handler := Auth(auth.NewVerifier(), tc.mode, nil)(next)

			r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
			if tc.bearer != "" {
				r.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, r)

			assert.Equal(t, tc.wantStatus, rr.Code)
			if tc.wantStatus == http.StatusOK {
				assert.True(t, reached, "handler should have been reached")
			} else {
				assert.False(t, reached, "handler must not be reached on 401")
			}
			assert.Equal(t, tc.wantUserID, gotHasUser)
			if tc.wantUserID {
				assert.Equal(t, tc.wantUserIDeq, gotUserID)
			}
		})
	}
}
