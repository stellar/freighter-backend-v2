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

// What a forged token gets in permissive mode, by how its timing claims look.
//
// This pins a deliberate and slightly uncomfortable consequence of permitting
// clock_ahead: Claims.Validate runs BEFORE signature verification, so a token
// signed with an attacker's key but merely dated into the future is classified
// clock_ahead and is served — it never reaches the signature check at all.
//
// That is safe, and the assertions below are what make it safe rather than
// merely asserted: the request is served with NO userID, so it is byte-for-byte
// equivalent to one carrying no Authorization header, which these routes already
// serve anonymously. An attacker gains nothing they could not have by sending no
// token. The cost is measurement, not access — invalid_permitted{clock_ahead} is
// an upper bound on fast-clock clients rather than an exact count.
//
// A forged token whose timing is otherwise fine still 401s on bad_signature, and
// one whose timing claims are malformed still 401s on bad_timing. Those two rows
// are the ones that would break if the permit predicate ever widened further.
func TestAuth_PermissiveForgedTokenOutcomes(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)
	_, attackerPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	mAndP := "GET " + authTestPath
	now := time.Now()

	cases := []struct {
		name       string
		token      string
		wantStatus int
		wantReason string
		wantResult string
	}{
		{
			// Timing is valid, so validation reaches the signature check and fails there.
			name:       "forged, valid timing -> 401 bad_signature",
			token:      mintToken(t, attackerPriv, sub, mAndP, auth.MaxTokenLifetime, now),
			wantStatus: http.StatusUnauthorized,
			wantReason: "bad_signature",
			wantResult: "rejected",
		},
		{
			// Over-long lifetime is bad_timing, which is NOT permitted.
			name:       "forged, malformed timing -> 401 bad_timing",
			token:      mintToken(t, attackerPriv, sub, mAndP, 2*time.Hour, now),
			wantStatus: http.StatusUnauthorized,
			wantReason: "bad_timing",
			wantResult: "rejected",
		},
		{
			// Indistinguishable from a real fast clock at this point in validation.
			// Served, but anonymously — see the doc comment above.
			name:       "forged, future-dated -> served anonymously (clock_ahead)",
			token:      mintToken(t, attackerPriv, sub, mAndP, auth.MaxTokenLifetime, now.Add(time.Hour)),
			wantStatus: http.StatusOK,
			wantReason: auth.ReasonClockAhead,
			wantResult: metrics.ResultInvalidPermitted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			m := metrics.NewAuth(reg)
			var hasUser bool
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, hasUser = auth.UserIDFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})
			handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Permissive, m)(next)

			r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
			r.Header.Set("Authorization", "Bearer "+tc.token)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, r)

			assert.Equal(t, tc.wantStatus, rr.Code)
			assert.Equal(t, float64(1),
				testutil.ToFloat64(m.RequestsTotal.WithLabelValues(tc.wantResult, tc.wantReason, "freighter-extension")),
				"expected one %s/%s", tc.wantResult, tc.wantReason)

			// The invariant that makes permitting a forged token safe: it is NEVER
			// authenticated. If this ever fails, a forged token has become an
			// identity, which is a different and much worse bug than a 401.
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
