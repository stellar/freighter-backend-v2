package auth

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwtgo "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMethodAndPath = "GET /api/v1/auth/whoami"

// newKeypair returns a fresh Ed25519 keypair and the hex-encoded public key
// (which is what the design uses as the JWT `sub` / user ID).
func newKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return pub, priv, hex.EncodeToString(pub)
}

// validClaims builds a well-formed claims set for the given subject and body.
func validClaims(sub, methodAndPath string, body []byte) Claims {
	now := time.Now()
	return Claims{
		BodyHash:      HashBody(body),
		MethodAndPath: methodAndPath,
		RegisteredClaims: jwtgo.RegisteredClaims{
			Subject:   sub,
			Issuer:    "freighter-extension",
			IssuedAt:  jwtgo.NewNumericDate(now),
			ExpiresAt: jwtgo.NewNumericDate(now.Add(MaxTokenLifetime)),
		},
	}
}

// mint signs claims with the given private key using EdDSA.
func mint(t *testing.T, priv ed25519.PrivateKey, claims Claims) string {
	t.Helper()
	tok := jwtgo.NewWithClaims(jwtgo.SigningMethodEdDSA, claims)
	s, err := tok.SignedString(priv)
	require.NoError(t, err)
	return s
}

func TestHashBody(t *testing.T) {
	// SHA-256 of empty input — the value the design doc calls out for GET requests.
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", HashBody(nil))
	assert.Equal(t, HashBody([]byte{}), HashBody(nil))
	assert.NotEqual(t, HashBody([]byte("a")), HashBody([]byte("b")))
}

func TestParseJWT_Valid(t *testing.T) {
	_, priv, sub := newKeypair(t)
	body := []byte(`{"hello":"world"}`)
	token := mint(t, priv, validClaims(sub, "PUT /api/v1/contacts", body))

	claims, err := ParseJWT(token, "PUT /api/v1/contacts", body)
	require.NoError(t, err)
	assert.Equal(t, sub, claims.Subject)
}

func TestParseJWT_CanonicalizesSubject(t *testing.T) {
	_, priv, sub := newKeypair(t)
	// Sign a token whose `sub` is uppercase hex. hex.DecodeString accepts it and
	// it verifies against the same key, but the returned user ID must be the
	// canonical lowercase form so callers can't split one key into two users.
	upper := strings.ToUpper(sub)
	token := mint(t, priv, validClaims(upper, testMethodAndPath, nil))

	claims, err := ParseJWT(token, testMethodAndPath, nil)
	require.NoError(t, err)
	assert.Equal(t, sub, claims.Subject)
	assert.Equal(t, claims.Subject, strings.ToLower(claims.Subject))
}

func TestParseJWT_WrongKey(t *testing.T) {
	pub1, _, sub1 := newKeypair(t)
	_, priv2, _ := newKeypair(t)
	require.NotEqual(t, pub1, priv2.Public())

	// Claims declare sub1, but the token is signed by priv2 → signature must not verify.
	token := mint(t, priv2, validClaims(sub1, testMethodAndPath, nil))

	_, err := ParseJWT(token, testMethodAndPath, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnauthorized)
}

func TestParseJWT_Tampered(t *testing.T) {
	_, priv, sub := newKeypair(t)
	token := mint(t, priv, validClaims(sub, testMethodAndPath, nil))

	// Flip a character in the payload segment.
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	if parts[1][0] == 'a' {
		parts[1] = "b" + parts[1][1:]
	} else {
		parts[1] = "a" + parts[1][1:]
	}
	tampered := strings.Join(parts, ".")

	_, err := ParseJWT(tampered, testMethodAndPath, nil)
	require.Error(t, err)
}

func TestParseJWT_Expired(t *testing.T) {
	_, priv, sub := newKeypair(t)
	c := validClaims(sub, testMethodAndPath, nil)
	past := time.Now().Add(-1 * time.Minute)
	c.IssuedAt = jwtgo.NewNumericDate(past)
	c.ExpiresAt = jwtgo.NewNumericDate(past.Add(MaxTokenLifetime))
	token := mint(t, priv, c)

	_, err := ParseJWT(token, testMethodAndPath, nil)
	require.Error(t, err)
	var expiredErr *ExpiredTokenError
	assert.ErrorAs(t, err, &expiredErr)
}

func TestParseJWT_LeewayWithinSkew(t *testing.T) {
	_, priv, sub := newKeypair(t)
	c := validClaims(sub, testMethodAndPath, nil)
	// Expired 3s ago — inside the ±5s clock-skew leeway, so it should still verify.
	exp := time.Now().Add(-3 * time.Second)
	c.IssuedAt = jwtgo.NewNumericDate(exp.Add(-MaxTokenLifetime))
	c.ExpiresAt = jwtgo.NewNumericDate(exp)
	token := mint(t, priv, c)

	_, err := ParseJWT(token, testMethodAndPath, nil)
	require.NoError(t, err)
}

func TestParseJWT_RejectsNonEdDSA(t *testing.T) {
	_, _, sub := newKeypair(t)
	c := validClaims(sub, testMethodAndPath, nil)
	// Sign with HMAC (alg=HS256) — an algorithm-confusion attempt.
	tok := jwtgo.NewWithClaims(jwtgo.SigningMethodHS256, c)
	token, err := tok.SignedString([]byte("attacker-symmetric-key"))
	require.NoError(t, err)

	_, err = ParseJWT(token, testMethodAndPath, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnauthorized)
}

func TestParseJWT_BodyHashMismatch(t *testing.T) {
	_, priv, sub := newKeypair(t)
	token := mint(t, priv, validClaims(sub, testMethodAndPath, []byte("signed-body")))

	_, err := ParseJWT(token, testMethodAndPath, []byte("different-body"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnauthorized)
}

func TestParseJWT_MethodAndPathMismatch(t *testing.T) {
	_, priv, sub := newKeypair(t)
	token := mint(t, priv, validClaims(sub, "GET /api/v1/a", nil))

	_, err := ParseJWT(token, "GET /api/v1/b", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnauthorized)
}

func TestParseJWT_BadSubjectEncoding(t *testing.T) {
	_, priv, _ := newKeypair(t)

	// Non-hex subject.
	token := mint(t, priv, validClaims("not-hex!!", testMethodAndPath, nil))
	_, err := ParseJWT(token, testMethodAndPath, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnauthorized)

	// Valid hex but wrong length for an Ed25519 key.
	token = mint(t, priv, validClaims(hex.EncodeToString([]byte("too-short")), testMethodAndPath, nil))
	_, err = ParseJWT(token, testMethodAndPath, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnauthorized)
}

func TestParseJWT_LifetimeTooLong(t *testing.T) {
	_, priv, sub := newKeypair(t)
	c := validClaims(sub, testMethodAndPath, nil)
	now := time.Now()
	c.IssuedAt = jwtgo.NewNumericDate(now)
	c.ExpiresAt = jwtgo.NewNumericDate(now.Add(1 * time.Hour)) // exp - iat well over the 15s cap
	token := mint(t, priv, c)

	_, err := ParseJWT(token, testMethodAndPath, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnauthorized)
}

// A token dated into the future beyond the clock-skew leeway must be rejected as
// bad timing — jwt/v5's WithLeeway alone does not reject a future iat, which
// would otherwise let a signer (iat=exp=now+lifetime+leeway) stretch the
// acceptance window past the intended ±skew.
func TestParseJWT_FutureIssuedAt(t *testing.T) {
	_, priv, sub := newKeypair(t)
	c := validClaims(sub, testMethodAndPath, nil)
	now := time.Now()
	// iat just past the skew window; keep exp within its own bound so the only
	// failing check is the new iat-skew check.
	c.IssuedAt = jwtgo.NewNumericDate(now.Add(ClockSkewLeeway + 2*time.Second))
	c.ExpiresAt = jwtgo.NewNumericDate(now.Add(ClockSkewLeeway + 2*time.Second))
	token := mint(t, priv, c)

	_, err := ParseJWT(token, testMethodAndPath, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnauthorized)
	assert.Equal(t, ReasonBadTiming, Reason(err))
}

// --- VerifyHTTPRequest ---

func newRequest(t *testing.T, method, target string, body []byte, bearer string) *http.Request {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	return r
}

func TestVerifyHTTPRequest_NoHeader(t *testing.T) {
	v := NewVerifier()
	_, err := v.VerifyHTTPRequest(newRequest(t, http.MethodGet, "/api/v1/auth/whoami", nil, ""))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoToken)
	// ErrNoToken must be distinguishable from a present-but-invalid token.
	assert.NotErrorIs(t, err, ErrUnauthorized)
}

func TestVerifyHTTPRequest_NonBearer(t *testing.T) {
	v := NewVerifier()
	r := newRequest(t, http.MethodGet, "/api/v1/auth/whoami", nil, "")
	r.Header.Set("Authorization", "Basic abc123")
	_, err := v.VerifyHTTPRequest(r)
	assert.ErrorIs(t, err, ErrNoToken)
}

func TestVerifyHTTPRequest_EmptyBearerRejected(t *testing.T) {
	v := NewVerifier()
	// A Bearer scheme with an empty/whitespace credential (or no credential at
	// all) is a present-but-invalid token: it must be rejected (401 in both
	// modes), not waved through as anonymous the way a missing token is.
	for _, header := range []string{"Bearer ", "Bearer", "Bearer    ", "bearer  "} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/whoami", nil)
		r.Header.Set("Authorization", header)

		_, err := v.VerifyHTTPRequest(r)
		require.Error(t, err, "header %q", header)
		assert.ErrorIs(t, err, ErrUnauthorized, "header %q", header)
		assert.NotErrorIs(t, err, ErrNoToken, "header %q", header)
		assert.Equal(t, ReasonMalformed, Reason(err), "header %q", header)
	}
}

func TestVerifyHTTPRequest_Valid(t *testing.T) {
	_, priv, sub := newKeypair(t)
	body := []byte(`{"x":1}`)
	token := mint(t, priv, validClaims(sub, "POST /api/v1/thing", body))

	v := NewVerifier()
	r := newRequest(t, http.MethodPost, "/api/v1/thing", body, token)

	userID, err := v.VerifyHTTPRequest(r)
	require.NoError(t, err)
	assert.Equal(t, sub, userID)

	// Body must remain readable by downstream handlers.
	got, err := readAll(r)
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestVerifyHTTPRequest_BindsQueryString(t *testing.T) {
	_, priv, sub := newKeypair(t)
	// Token signed for the path including its query string.
	token := mint(t, priv, validClaims(sub, "GET /api/v1/thing?a=1", nil))

	v := NewVerifier()
	// Same path, different query → methodAndPath mismatch → reject.
	_, err := v.VerifyHTTPRequest(newRequest(t, http.MethodGet, "/api/v1/thing?a=2", nil, token))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnauthorized)
}

func TestVerifyHTTPRequest_CaseInsensitiveBearer(t *testing.T) {
	_, priv, sub := newKeypair(t)
	token := mint(t, priv, validClaims(sub, "GET /api/v1/auth/whoami", nil))

	v := NewVerifier()
	r := newRequest(t, http.MethodGet, "/api/v1/auth/whoami", nil, "")
	// RFC 6750: the auth scheme is case-insensitive.
	r.Header.Set("Authorization", "bearer "+token)

	userID, err := v.VerifyHTTPRequest(r)
	require.NoError(t, err)
	assert.Equal(t, sub, userID)
}

func TestVerifyHTTPRequest_LargeBodyNotTruncated(t *testing.T) {
	_, priv, sub := newKeypair(t)
	// Larger than the verifier's former 256KB default; the full body must be
	// hashed (no truncation), otherwise bodyHash would mismatch and 401.
	body := bytes.Repeat([]byte("x"), 300*1024)
	token := mint(t, priv, validClaims(sub, "POST /api/v1/thing", body))

	v := NewVerifier()
	userID, err := v.VerifyHTTPRequest(newRequest(t, http.MethodPost, "/api/v1/thing", body, token))
	require.NoError(t, err)
	assert.Equal(t, sub, userID)
}

func TestReason(t *testing.T) {
	_, priv, sub := newKeypair(t)
	_, otherPriv, _ := newKeypair(t)

	t.Run("malformed", func(t *testing.T) {
		_, err := ParseJWT("not-a-jwt", testMethodAndPath, nil)
		assert.Equal(t, ReasonMalformed, Reason(err))
	})
	t.Run("bad_body_hash", func(t *testing.T) {
		token := mint(t, priv, validClaims(sub, testMethodAndPath, []byte("signed")))
		_, err := ParseJWT(token, testMethodAndPath, []byte("different"))
		assert.Equal(t, ReasonBadBodyHash, Reason(err))
	})
	t.Run("bad_method_path", func(t *testing.T) {
		token := mint(t, priv, validClaims(sub, "GET /api/v1/a", nil))
		_, err := ParseJWT(token, "GET /api/v1/b", nil)
		assert.Equal(t, ReasonBadMethodPath, Reason(err))
	})
	t.Run("bad_timing", func(t *testing.T) {
		c := validClaims(sub, testMethodAndPath, nil)
		now := time.Now()
		c.IssuedAt = jwtgo.NewNumericDate(now)
		c.ExpiresAt = jwtgo.NewNumericDate(now.Add(time.Hour)) // lifetime far over the cap
		token := mint(t, priv, c)
		_, err := ParseJWT(token, testMethodAndPath, nil)
		assert.Equal(t, ReasonBadTiming, Reason(err))
	})
	t.Run("bad_subject", func(t *testing.T) {
		token := mint(t, priv, validClaims("not-hex!!", testMethodAndPath, nil))
		_, err := ParseJWT(token, testMethodAndPath, nil)
		assert.Equal(t, ReasonBadSubject, Reason(err))
	})
	t.Run("bad_signature", func(t *testing.T) {
		token := mint(t, otherPriv, validClaims(sub, testMethodAndPath, nil))
		_, err := ParseJWT(token, testMethodAndPath, nil)
		assert.Equal(t, ReasonBadSignature, Reason(err))
	})
	t.Run("expired", func(t *testing.T) {
		c := validClaims(sub, testMethodAndPath, nil)
		past := time.Now().Add(-time.Minute)
		c.IssuedAt = jwtgo.NewNumericDate(past)
		c.ExpiresAt = jwtgo.NewNumericDate(past.Add(MaxTokenLifetime))
		token := mint(t, priv, c)
		_, err := ParseJWT(token, testMethodAndPath, nil)
		assert.Equal(t, ReasonExpired, Reason(err))
	})
}

func TestContextRoundTrip(t *testing.T) {
	ctx := ContextWithUserID(t.Context(), "deadbeef")
	id, ok := UserIDFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "deadbeef", id)

	_, ok = UserIDFromContext(t.Context())
	assert.False(t, ok)
}

// readAll drains the (possibly reset) request body.
func readAll(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}
