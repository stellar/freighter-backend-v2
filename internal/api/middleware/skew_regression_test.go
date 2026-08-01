package middleware

import (
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
	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

// The clock offsets actually observed in wallet-eng-prd during the first 24h of
// real JWT traffic (#147), recovered from the `token expired by <d>` detail on
// each rejection log line. Every one is a lagging clock; `bad_timing` (the
// fast-clock reason) was 0 over the same window.
//
// These are not synthetic bounds — they are five distinct devices, each offset
// stable to within a second across hundreds of requests, so they are fixed
// misconfigurations rather than drift. The 8h entry is exact, consistent with a
// timezone offset written as absolute time.
var observedProdClockLags = []struct {
	name string
	lag  time.Duration
}{
	{"3m04s (nearest miss, just past the 2m leeway)", 3*time.Minute + 4*time.Second},
	{"11m05s", 11*time.Minute + 5*time.Second},
	{"15m14s", 15*time.Minute + 14*time.Second},
	{"3h01m20s (67% of rejections)", 3*time.Hour + 1*time.Minute + 20*time.Second},
	{"8h00m00s (exact — timezone as absolute time)", 8 * time.Hour},
}

// Every clock offset that locked a user out in prod must now be served instead of
// rejected. This is the regression test for #147 itself: it pins the fix to the
// real-world data rather than to a bound someone picked, so a future change to
// the leeway, the predicate, or the reason classification that would re-break
// these users fails here with the offset named.
func TestAuth_PermissiveServesEveryObservedProdClockLag(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	for _, tc := range observedProdClockLags {
		t.Run(tc.name, func(t *testing.T) {
			// A client whose clock lags by tc.lag stamps iat/exp that far behind.
			token := mintToken(t, priv, sub, "GET "+authTestPath, auth.MaxTokenLifetime, time.Now().Add(-tc.lag))

			reg := prometheus.NewRegistry()
			m := metrics.NewAuth(reg)
			reached := false
			var gotUserID string
			var hasUser bool
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				gotUserID, hasUser = auth.UserIDFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})
			handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Permissive, m)(next)

			r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, r)

			// Served, not rejected — this is the whole point of the change.
			assert.Equal(t, http.StatusOK, rr.Code, "a %s lagging clock must be served, not 401ed", tc.name)
			assert.True(t, reached, "handler must be reached")

			// Served anonymously: authentication still failed, so no identity is
			// asserted downstream. Callers that need identity must not silently
			// treat these as authenticated.
			assert.False(t, hasUser, "no userID may be attached — authentication failed")
			assert.Empty(t, gotUserID)

			// Counted as permitted skew, so the rate stays visible and the
			// permissive->strict readiness check can still see this population.
			assert.Equal(t, float64(1),
				testutil.ToFloat64(m.RequestsTotal.WithLabelValues(
					metrics.ResultInvalidPermitted, "expired", "freighter-extension")),
				"must be counted as invalid_permitted/expired")
		})
	}
}

// Sanity floor: a lag far beyond anything observed is still served, so the fix
// is not quietly bounded by some larger threshold the way a leeway would be.
// This is the property that distinguishes it from widening --auth-clock-skew-leeway.
func TestAuth_PermissiveServesArbitrarilyLargeClockLag(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	for _, lag := range []time.Duration{24 * time.Hour, 30 * 24 * time.Hour, 365 * 24 * time.Hour} {
		token := mintToken(t, priv, sub, "GET "+authTestPath, auth.MaxTokenLifetime, time.Now().Add(-lag))
		reached := false
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true; w.WriteHeader(http.StatusOK) })
		handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Permissive, nil)(next)

		r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, r)

		assert.Equal(t, http.StatusOK, rr.Code, "lag %s must be served", lag)
		assert.True(t, reached, "handler must be reached for lag %s", lag)
	}
}
