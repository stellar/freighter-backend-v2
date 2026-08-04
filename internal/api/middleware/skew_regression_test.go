package middleware

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwtgo "github.com/golang-jwt/jwt/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

// The clock offsets actually observed in wallet-eng-prd (#147), recovered from
// the `token expired by <d>` detail on each rejection log line and converted to
// client lag (lag = expired_by + the 15s token lifetime).
//
// These are not synthetic bounds — each offset is stable to within a second
// across hundreds of requests, and stable per device across days, so they are
// fixed misconfigurations rather than drift. Verified as genuine wrong clocks
// rather than stale tokens replayed from a retry queue: within a single burst
// the offset stays flat to ±0.5s over spans up to 478s, whereas a replayed token
// would drift 1s per elapsed second. The 8h entry is exact, consistent with a
// timezone offset written as absolute time.
//
// Measured over the 7 days ending 2026-08-03: 524 rejections, 442 expired
// (84.4%), 78 clock_ahead (14.9%), 4 malformed (0.8%). NOTE for anyone editing
// this list: an earlier revision of this file said fast clocks were 0 in prod.
// That was true of a single 24h window and is now false — see the fast-clock
// note on TestAuth_PermissiveServesArbitrarilyLargeClockOffsetEitherDirection.
var observedProdClockLags = []struct {
	name string
	lag  time.Duration
}{
	{"3m04s (nearest miss, just past the 2m leeway)", 3*time.Minute + 4*time.Second},
	{"11m05s", 11*time.Minute + 5*time.Second},
	{"15m14s", 15*time.Minute + 14*time.Second},
	{"3h01m20s (50.7% of rejections)", 3*time.Hour + 1*time.Minute + 20*time.Second},
	{"8h00m00s (exact — timezone as absolute time)", 8 * time.Hour},
	{"19h06m (observed in the 7d window)", 19*time.Hour + 6*time.Minute},
	// ~19.8 days stale. The single most useful row here: no leeway anyone would
	// accept covers it, which is the whole argument against tuning a bound.
	{"475h14m (~19.8 days — clock left far in the past)", 475*time.Hour + 14*time.Minute},
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

// A permitted request must reach its handler with its body still readable.
//
// Before the fall-through, next.ServeHTTP was reachable from exactly two arms:
// success (body read and reset) and ErrNoToken in permissive (which returns
// before the body is ever read). So the invariant was "if the verifier touched
// the body, verification succeeded". The permitted break retires it — a handler
// now runs after parseJWT returned an error, on a request the verifier already
// drained. See TestVerifyHTTPRequest_ResetsBodyOnValidationFailure in
// internal/auth for the unit-level guard on the reset itself.
//
// This is sized by which routes ride on it: of the four endpoints affected in
// prd, three are POSTs with bodies — token-prices, collectibles and
// ledger-key/accounts. That is the majority of this change's payload, and it was
// the untested half of the new path. A regression here surfaces as io.EOF inside
// the handler (a 400, a 500, or an empty-result 200 depending on the handler) and
// only for skewed-clock users — the population already labelled broken, which is
// precisely what would stop anyone from investigating.
//
// Both directions are covered with a bodyHash bound to the real body. The fast
// clock alone would pass even with a mismatched hash, because Claims.Validate
// returns clock_ahead before it reaches the bodyHash check — so testing only that
// direction would leave the bodyHash-verified path (expired) unexercised.
func TestAuth_PermittedRequestReachesHandlerWithIntactBody(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	body := []byte(`{"addresses":["GABC"]}`)
	mAndP := "POST " + authTestPath

	for _, tc := range []struct {
		name       string
		issuedAt   time.Time
		wantReason string
	}{
		{"lagging clock (expired)", time.Now().Add(-time.Hour), "expired"},
		{"fast clock (clock_ahead)", time.Now().Add(time.Hour), auth.ReasonClockAhead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := mintTokenWithBody(t, priv, sub, mAndP, auth.MaxTokenLifetime, tc.issuedAt, body)

			reg := prometheus.NewRegistry()
			m := metrics.NewAuth(reg)
			var got []byte
			var readErr error
			var hasUser bool
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, readErr = io.ReadAll(r.Body)
				_, hasUser = auth.UserIDFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})
			handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Permissive, m)(next)

			r := httptest.NewRequest(http.MethodPost, authTestPath, bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, r)

			require.Equal(t, http.StatusOK, rr.Code)
			require.NoError(t, readErr)
			assert.Equal(t, body, got, "a permitted request must reach its handler with an intact body")
			assert.False(t, hasUser, "no userID may be attached — authentication failed")
			assert.Equal(t, float64(1),
				testutil.ToFloat64(m.RequestsTotal.WithLabelValues(
					metrics.ResultInvalidPermitted, tc.wantReason, "freighter-extension")))
		})
	}
}

// stubVerifier returns a fixed error, so a test can hand the middleware an error
// shape the real verifier does not currently produce.
type stubVerifier struct{ err error }

func (s stubVerifier) VerifyHTTPRequest(*http.Request) (auth.Identity, error) {
	return auth.Identity{}, s.err
}

// The fail-open set is the two clock ERROR TYPES, not "any error whose reason
// string happens to be a clock reason".
//
// This is the regression guard for the mechanism that already failed once in this
// branch: 5b32ac7 keyed the predicate on the reason string and permitted
// ReasonBadTiming, serving tokens with no exp claim at all and counting them as
// invalid_permitted. Review caught it; no test did.
//
// The next instance would be equally invisible. auth.Reason() flattens a
// *VerificationError to its plain Reason string, that field is set at ten sites
// across internal/auth, and the prevailing idiom in claims.go is
// &VerificationError{Reason: ...} (6 of 8 returns). Concretely: Claims.Validate
// does not check nbf, and a future nbf IS a fast-clock symptom — so whoever adds
// that check reaches for ReasonClockAhead in the file's own style and, under a
// string predicate, silently adds a pre-signature fail-open path that reads as
// correct in review. Requiring a dedicated error type makes that a diff someone
// has to make here, in this file.
//
// The stakes are the metric, not access: no userID attaches either way. But
// invalid_permitted is what gates the permissive->strict flip, and contaminating
// it with malformed tokens is exactly the harm 0c1478b was written to undo.
func TestAuth_PermissiveDoesNotFailOpenOnReasonStringAlone(t *testing.T) {
	for _, reason := range []string{auth.ReasonExpired, auth.ReasonClockAhead} {
		t.Run(reason, func(t *testing.T) {
			// A *VerificationError carrying a clock reason string, but NOT one of the
			// two dedicated clock error types.
			err := &auth.VerificationError{Reason: reason, Err: errors.New("synthetic")}
			require.Equal(t, reason, auth.Reason(err), "precondition: Reason() flattens to the clock string")
			require.ErrorIs(t, err, auth.ErrUnauthorized, "precondition: routes through the ErrUnauthorized arm")

			reg := prometheus.NewRegistry()
			m := metrics.NewAuth(reg)
			reached := false
			handler := Auth(stubVerifier{err: err}, auth.Permissive, m)(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					reached = true
					w.WriteHeader(http.StatusOK)
				}))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, authTestPath, nil))

			assert.Equal(t, http.StatusUnauthorized, rr.Code,
				"a clock reason STRING without the clock error type must still 401")
			assert.False(t, reached, "handler must not be reached")
			// The stub request carries no Authorization header, so the unverified
			// issuer is "" and SanitizeClient buckets it as "other" (not ClientNone,
			// which is reserved for the no-token arm).
			const client = "other"
			assert.Equal(t, float64(1),
				testutil.ToFloat64(m.RequestsTotal.WithLabelValues("rejected", reason, client)),
				"must count as rejected, not invalid_permitted")
			assert.Equal(t, float64(0),
				testutil.ToFloat64(m.RequestsTotal.WithLabelValues(
					metrics.ResultInvalidPermitted, reason, client)),
				"must not appear in the counter that gates the permissive->strict flip")
		})
	}
}

// The `iss` label on a permitted EXPIRED request must come from the
// signature-verified claims, not from the unverified re-parse.
//
// The permitted fall-through moved this label onto the serving path, and
// IssuerFromRequestUnverified's own contract says not to rely on it there. For an
// expired token an authentic issuer exists — ParseWithClaims verified the
// signature before jwt/v5 validated claims — so ExpiredTokenError carries it and
// the middleware prefers it.
//
// Why it matters: the design doc gates the permissive->strict flip on the timing
// reasons reaching ~0 per `iss`, and that per-client split is the number used to
// decide which client team still needs to ship the Date-header offset fix. In
// aggregate an inflated count fails safe (it can only read "not ready"); split by
// client it is simply wrong in whichever direction the sender chose.
//
// The test signs a token whose iss claims to be the mobile client. Because the
// signature covers the claims, that iss IS authentic here — the point is that the
// middleware reads the verified value rather than re-deriving it, so a
// header-only rewrite cannot move the count between client buckets.
func TestAuth_PermittedExpiredUsesSignatureVerifiedIssuer(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	// A token signed with iss="freighter-mobile", lagging well past the leeway.
	past := time.Now().Add(-time.Hour)
	claims := auth.Claims{
		BodyHash:      auth.HashBody(nil),
		MethodAndPath: "GET " + authTestPath,
		RegisteredClaims: jwtgo.RegisteredClaims{
			Subject:   sub,
			Issuer:    "freighter-mobile",
			IssuedAt:  jwtgo.NewNumericDate(past),
			ExpiresAt: jwtgo.NewNumericDate(past.Add(auth.MaxTokenLifetime)),
		},
	}
	token, err := jwtgo.NewWithClaims(jwtgo.SigningMethodEdDSA, claims).SignedString(priv)
	require.NoError(t, err)

	reg := prometheus.NewRegistry()
	m := metrics.NewAuth(reg)
	handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Permissive, m)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, float64(1),
		testutil.ToFloat64(m.RequestsTotal.WithLabelValues(
			metrics.ResultInvalidPermitted, "expired", "freighter-mobile")),
		"the permitted expired request must be attributed to the signature-verified iss")

	// And the error itself must expose that verified issuer, which is what makes
	// the middleware able to prefer it over the unverified re-parse.
	_, verifyErr := auth.NewVerifier(auth.ClockSkewLeeway).VerifyHTTPRequest(
		func() *http.Request {
			rr := httptest.NewRequest(http.MethodGet, authTestPath, nil)
			rr.Header.Set("Authorization", "Bearer "+token)
			return rr
		}())
	var expiredErr *auth.ExpiredTokenError
	require.ErrorAs(t, verifyErr, &expiredErr)
	assert.Equal(t, "freighter-mobile", expiredErr.Issuer)
}

// The clock_ahead half of the same property. It could not be had while
// Claims.Validate ran ahead of the signature — there was no verified issuer to
// carry — and it is the half that matters most now: over the 7d prod sample
// clock_ahead is the majority of permitted traffic on the most recent days, so
// leaving it on the unverified re-parse would leave the larger share of the
// per-client split spoofable by the sender.
func TestAuth_PermittedClockAheadUsesSignatureVerifiedIssuer(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	// A token signed with iss="freighter-mobile", dated well past the leeway.
	future := time.Now().Add(time.Hour)
	claims := auth.Claims{
		BodyHash:      auth.HashBody(nil),
		MethodAndPath: "GET " + authTestPath,
		RegisteredClaims: jwtgo.RegisteredClaims{
			Subject:   sub,
			Issuer:    "freighter-mobile",
			IssuedAt:  jwtgo.NewNumericDate(future),
			ExpiresAt: jwtgo.NewNumericDate(future.Add(auth.MaxTokenLifetime)),
		},
	}
	token, err := jwtgo.NewWithClaims(jwtgo.SigningMethodEdDSA, claims).SignedString(priv)
	require.NoError(t, err)

	reg := prometheus.NewRegistry()
	m := metrics.NewAuth(reg)
	handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Permissive, m)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, float64(1),
		testutil.ToFloat64(m.RequestsTotal.WithLabelValues(
			metrics.ResultInvalidPermitted, auth.ReasonClockAhead, "freighter-mobile")),
		"the permitted clock_ahead request must be attributed to the signature-verified iss")

	// The load-bearing assertion. The metric row above would pass on the
	// unverified re-parse too, since it reads the same bearer token; this one only
	// passes if the issuer was carried out of the verified claims.
	_, verifyErr := auth.NewVerifier(auth.ClockSkewLeeway).VerifyHTTPRequest(
		func() *http.Request {
			rr := httptest.NewRequest(http.MethodGet, authTestPath, nil)
			rr.Header.Set("Authorization", "Bearer "+token)
			return rr
		}())
	var clockAheadErr *auth.ClockAheadError
	require.ErrorAs(t, verifyErr, &clockAheadErr)
	assert.Equal(t, "freighter-mobile", clockAheadErr.Issuer)
}

// Sanity floor, in BOTH directions: an offset far beyond anything observed is
// still served, so the fix is not quietly bounded by some larger threshold the
// way a leeway would be. This is the property that distinguishes it from
// widening --auth-clock-skew-leeway — there is no number left to re-tune,
// because freshness is no longer what decides whether these requests are
// answered.
//
// The negative (fast-clock) cases are no longer hypothetical. They were added
// when prod had shown only lagging clocks, on the reasoning that 5 devices could
// not support a conclusion that fast clocks do not occur. That reasoning held:
// over the 7 days ending 2026-08-03 prd logged 78 clock_ahead rejections against
// 442 expired, and clock_ahead was the MAJORITY on the two most recent days
// (08-02: 38 vs 44; 08-03: 20 vs 8). The trend is toward fast clocks.
//
// So these rows now guard a live population, not a symmetry argument — which
// also makes the expired/clock_ahead split load-bearing rather than tidy: had
// this shipped permitting only `expired`, ~15% of rejections and rising would
// still be 401ing.
func TestAuth_PermissiveServesArbitrarilyLargeClockOffsetEitherDirection(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	offsets := []time.Duration{
		-365 * 24 * time.Hour, -30 * 24 * time.Hour, -24 * time.Hour, // lagging  -> expired
		24 * time.Hour, 30 * 24 * time.Hour, 365 * 24 * time.Hour, // fast     -> clock_ahead
	}

	for _, offset := range offsets {
		direction, wantReason := "lagging", "expired"
		if offset > 0 {
			direction, wantReason = "fast", auth.ReasonClockAhead
		}

		reg := prometheus.NewRegistry()
		m := metrics.NewAuth(reg)
		token := mintToken(t, priv, sub, "GET "+authTestPath, auth.MaxTokenLifetime, time.Now().Add(offset))

		reached, hasUser := false, false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached = true
			_, hasUser = auth.UserIDFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})
		handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Permissive, m)(next)

		r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, r)

		assert.Equal(t, http.StatusOK, rr.Code, "%s clock by %s must be served", direction, offset)
		assert.True(t, reached, "handler must be reached for %s %s", direction, offset)
		assert.False(t, hasUser, "no userID for %s %s", direction, offset)
		assert.Equal(t, float64(1),
			testutil.ToFloat64(m.RequestsTotal.WithLabelValues(
				metrics.ResultInvalidPermitted, wantReason, "freighter-extension")),
			"%s clock by %s must count as invalid_permitted/%s", direction, offset, wantReason)
	}
}

// The complement: a token whose timing claims are malformed rather than merely
// wrong is still rejected in permissive. These are the cases left in
// ReasonBadTiming after the clock reasons were split out — no real client emits
// them, and serving them would make the permitted-skew rate mean nothing.
func TestAuth_PermissiveStillRejectsMalformedTiming(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	// exp - iat far beyond MaxTokenLifetime: well-formed claims, but an attempt to
	// mint a long-lived token rather than a clock that is merely wrong.
	token := mintToken(t, priv, sub, "GET "+authTestPath, 2*time.Hour, time.Now())

	reg := prometheus.NewRegistry()
	m := metrics.NewAuth(reg)
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true; w.WriteHeader(http.StatusOK) })
	handler := Auth(auth.NewVerifier(auth.ClockSkewLeeway), auth.Permissive, m)(next)

	r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	assert.Equal(t, http.StatusUnauthorized, rr.Code, "an over-long lifetime must still 401 in permissive")
	assert.False(t, reached, "handler must not be reached")
	assert.Equal(t, float64(1),
		testutil.ToFloat64(m.RequestsTotal.WithLabelValues("rejected", "bad_timing", "freighter-extension")),
		"must be counted as a rejection, not permitted skew")
}

// The permit path must be reachable only by someone holding sub's private key. A
// token dated into the future but signed by a different key is a forgery, not a
// fast clock — and since the reason drives both the 200 and the counter the
// permissive->strict decision reads, letting it through would mean any
// unauthenticated caller could manufacture the appearance of skewed clients and
// hold the rollout open indefinitely.
func TestAuth_PermissiveRejectsForgedFutureDatedToken(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	_, attackerPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	// Exactly the claims a fast clock produces, signed by a key that is not sub.
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

	assert.Equal(t, http.StatusUnauthorized, rr.Code, "a forged future-dated token must 401, not be served as skew")
	assert.False(t, reached, "handler must not be reached")
	assert.Equal(t, float64(1),
		testutil.ToFloat64(m.RequestsTotal.WithLabelValues("rejected", auth.ReasonBadSignature, "freighter-extension")),
		"must count as rejected/bad_signature")
	assert.Equal(t, float64(0),
		testutil.ToFloat64(m.RequestsTotal.WithLabelValues(
			metrics.ResultInvalidPermitted, auth.ReasonClockAhead, "freighter-extension")),
		"must not contaminate the counter that gates the permissive->strict flip")
}
