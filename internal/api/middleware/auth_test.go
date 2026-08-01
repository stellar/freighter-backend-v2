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
		// Issued an hour ago: expired well beyond any configured clock-skew leeway.
		return mintToken(t, priv, sub, methodAndPath, auth.MaxTokenLifetime, now.Add(-1*time.Hour))
	}
	// Signed by a different key than the one declared in sub.
	wrongKeyToken := func() string { return mintToken(t, otherPriv, sub, methodAndPath, auth.MaxTokenLifetime, now) }
	// Dated an hour into the future: fails the iat/exp future bounds in
	// Claims.Validate, i.e. ReasonBadTiming rather than ReasonExpired. This is the
	// fast-clock counterpart of expiredToken.
	futureToken := func() string {
		return mintToken(t, priv, sub, methodAndPath, auth.MaxTokenLifetime, now.Add(1*time.Hour))
	}

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
		// An expired token is served anonymously in permissive: the routes it guards
		// already serve anonymous traffic, so 401ing a client that would have
		// succeeded sending no token at all locks out users whose clock lags. No
		// userID is attached — this is a failed authentication, not a successful one.
		{"permissive/expired", auth.Permissive, expiredToken(), http.StatusOK, false, ""},
		// A future-dated token is classified bad_timing, which Claims.Validate
		// produces BEFORE signature verification — so it proves nothing about who
		// signed it and stays a 401 even in permissive. This is the fast-clock gap:
		// deliberate, and measured at 0 occurrences in prod (see auth.go).
		{"permissive/future-dated", auth.Permissive, futureToken(), http.StatusUnauthorized, false, ""},
		// Non-timing failures stay 401 in permissive: a bad signature is a real bug
		// or attack, not a wrong clock, and must remain loud.
		{"permissive/wrong-key", auth.Permissive, wrongKeyToken(), http.StatusUnauthorized, false, ""},
		{"required/no-header", auth.Required, "", http.StatusUnauthorized, false, ""},
		{"required/valid", auth.Required, validToken(), http.StatusOK, true, sub},
		// Strict stays fail-closed for every invalid token, timing included.
		{"required/expired", auth.Required, expiredToken(), http.StatusUnauthorized, false, ""},
		{"required/future-dated", auth.Required, futureToken(), http.StatusUnauthorized, false, ""},
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

			handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), tc.mode, nil)(next)

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

// A token that is BOTH forged and badly timed must still be rejected in
// permissive mode. Claims.Validate runs before signature verification in
// parseJWT, so a wrong-key token whose iat is far in the future is classified
// ReasonBadTiming and never reaches the signature check at all. If the
// permissive fall-through keyed on bad_timing, an attacker-signed token would be
// waved through on a reason that carries no proof the signature was ever
// checked. Permitting grants no userID, so this is not a privilege escalation —
// but it would make the "a bad signature always stays loud" contract false and
// pollute the invalid_permitted counter that gates the strict flip.
func TestAuth_PermissiveRejectsForgedTokenWithBadTiming(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)
	_, attackerPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	// Signed by a key that is NOT sub's, and dated an hour ahead.
	token := mintToken(t, attackerPriv, sub, "GET "+authTestPath, auth.MaxTokenLifetime, time.Now().Add(time.Hour))

	reg := prometheus.NewRegistry()
	m := metrics.NewAuth(reg)
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true; w.WriteHeader(http.StatusOK) })
	handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Permissive, m)(next)

	r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	assert.Equal(t, http.StatusUnauthorized, rr.Code, "forged + badly-timed token must 401 in permissive")
	assert.False(t, reached, "handler must not be reached")
	assert.Equal(t, float64(0),
		testutil.ToFloat64(m.RequestsTotal.WithLabelValues(metrics.ResultInvalidPermitted, "bad_timing", "freighter-extension")),
		"a forged token must never be counted as permitted skew")
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
	handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Required, nil)(next)
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
	wrongKey := mintToken(t, otherPriv, sub, mAndP, auth.MaxTokenLifetime, now)           // parses, bad signature
	expired := mintToken(t, priv, sub, mAndP, auth.MaxTokenLifetime, now.Add(-time.Hour)) // valid signature, stale clock

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
		// Timing failures served anonymously in permissive get their own result
		// label, so the skew rate stays separately measurable — an alert or a
		// strict-flip readiness check on result="rejected" alone would otherwise
		// read these as having disappeared rather than as still happening.
		{"permitted expired", expired, metrics.ResultInvalidPermitted, "expired", "freighter-extension"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			m := metrics.NewAuth(reg)
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Permissive, m)(next)

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
