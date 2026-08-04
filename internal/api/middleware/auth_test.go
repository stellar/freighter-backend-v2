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

// mintToken mints a token for a nil-body (GET) request. Use mintTokenWithBody
// when the request carries one — the bodyHash claim must match it.
func mintToken(t *testing.T, priv ed25519.PrivateKey, sub, methodAndPath string, lifetime time.Duration, issuedAt time.Time) string {
	t.Helper()
	return authtest.MintToken(t, priv, sub, methodAndPath, lifetime, issuedAt, nil)
}

func mintTokenWithBody(t *testing.T, priv ed25519.PrivateKey, sub, methodAndPath string, lifetime time.Duration, issuedAt time.Time, body []byte) string {
	t.Helper()
	return authtest.MintToken(t, priv, sub, methodAndPath, lifetime, issuedAt, body)
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
	// Claims.Validate, i.e. ReasonClockAhead. The fast-clock counterpart of
	// expiredToken.
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
		// A fast clock is served too — clock_ahead is the mirror of expired, and
		// permissive mode should not care which direction a wrong clock is wrong in.
		{"permissive/future-dated", auth.Permissive, futureToken(), http.StatusOK, false, ""},
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

// A forged token gets exactly one outcome in permissive mode — 401 bad_signature —
// whatever its timing claims say.
//
// The uniformity is the property worth pinning. The signature is verified before
// any claim is inspected, so an attacker cannot steer the classification by
// choosing claims: dating a token into the future no longer buys the clock_ahead
// reason (and with it a served 200 and a tick on the counter that gates the
// permissive→strict flip), and an over-long lifetime no longer buys bad_timing.
//
// Each row below produced a different outcome before the signature gate was moved
// ahead of Claims.Validate. If any of them ever diverges again, a claims check has
// moved back in front of the signature and whatever reason it yields is no longer
// evidence of a real user.
func TestAuth_PermissiveForgedTokenOutcomes(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)
	_, attackerPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	mAndP := "GET " + authTestPath
	now := time.Now()

	cases := []struct {
		name  string
		token string
	}{
		{"valid timing", mintToken(t, attackerPriv, sub, mAndP, auth.MaxTokenLifetime, now)},
		{"over-long lifetime (would-be bad_timing)", mintToken(t, attackerPriv, sub, mAndP, 2*time.Hour, now)},
		{"future-dated (would-be clock_ahead)", mintToken(t, attackerPriv, sub, mAndP, auth.MaxTokenLifetime, now.Add(time.Hour))},
		{"long expired (would-be expired)", mintToken(t, attackerPriv, sub, mAndP, auth.MaxTokenLifetime, now.Add(-time.Hour))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			m := metrics.NewAuth(reg)
			var hasUser bool
			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				_, hasUser = auth.UserIDFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})
			handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Permissive, m)(next)

			r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
			r.Header.Set("Authorization", "Bearer "+tc.token)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, r)

			assert.Equal(t, http.StatusUnauthorized, rr.Code, "a forged token must 401 regardless of its claims")
			assert.False(t, reached, "handler must not be reached")
			assert.Equal(t, float64(1),
				testutil.ToFloat64(m.RequestsTotal.WithLabelValues("rejected", auth.ReasonBadSignature, "freighter-extension")),
				"expected one rejected/bad_signature")

			// The invariant that matters most: a forged token is NEVER authenticated.
			// If this ever fails, a forgery has become an identity, which is a
			// different and much worse bug than a 401.
			assert.False(t, hasUser, "a forged token must never yield a userID")
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
