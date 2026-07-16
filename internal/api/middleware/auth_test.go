package middleware

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/auth/authtest"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

const authTestPath = "/api/v1/auth/whoami"

func mintToken(t *testing.T, priv ed25519.PrivateKey, sub, methodAndPath string, lifetime time.Duration, issuedAt time.Time) string {
	t.Helper()
	return authtest.MintToken(t, priv, sub, methodAndPath, lifetime, issuedAt)
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

// When the request body exceeds the limit installed upstream by BodySizeLimit,
// the verifier's io.ReadAll returns an *http.MaxBytesError. The middleware must
// surface that as a 413 (client error), not a 500.
func TestAuth_OversizedBodyReturns413(t *testing.T) {
	const limit = 16

	body := bytes.NewReader(make([]byte, limit+1))
	r := httptest.NewRequest(http.MethodPost, authTestPath, body)
	r.Header.Set("Authorization", "Bearer token-value-is-irrelevant-body-is-read-first")
	rr := httptest.NewRecorder()
	// Simulate the upstream BodySizeLimit middleware wrapping the body.
	r.Body = http.MaxBytesReader(rr, r.Body, limit)

	reached := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
	handler := Auth(auth.NewVerifier(), auth.Required, nil)(next)
	handler.ServeHTTP(rr, r)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	assert.False(t, reached, "handler must not be reached when the body exceeds the limit")
}

func TestAuth_ClientLabel(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)
	_, otherPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	mAndP := "GET " + authTestPath
	now := time.Now()
	valid := mintToken(t, priv, sub, mAndP, auth.MaxTokenLifetime, now)
	wrongKey := mintToken(t, otherPriv, sub, mAndP, auth.MaxTokenLifetime, now) // parses, bad signature

	cases := []struct {
		name       string
		bearer     string
		wantResult string
		wantReason string
		wantClient string
	}{
		{"authenticated", valid, "authenticated", "ok", "freighter-extension"},
		{"anonymous", "", "anonymous", "no_token", "none"},
		{"rejected readable iss", wrongKey, "rejected", "bad_signature", "freighter-extension"},
		{"rejected malformed", "not-a-jwt", "rejected", "malformed", "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			m := metrics.NewAuth(reg)
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			handler := Auth(auth.NewVerifier(), auth.Permissive, m)(next)

			r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
			if tc.bearer != "" {
				r.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			handler.ServeHTTP(httptest.NewRecorder(), r)

			got := testutil.ToFloat64(m.RequestsTotal.WithLabelValues(tc.wantResult, tc.wantReason, tc.wantClient))
			assert.Equal(t, float64(1), got, "expected one %s/%s/%s", tc.wantResult, tc.wantReason, tc.wantClient)
		})
	}
}
