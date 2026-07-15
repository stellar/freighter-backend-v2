package middleware

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/logger"
)

// Continuity guard (spec §5.1): an anonymous request must emit exactly the
// pre-existing keys and message, and NO auth fields.
func TestLogging_AnonymousLineUnchanged(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := Logging()(next)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil))

	out := buf.String()
	assert.Contains(t, out, "Request completed")
	// Existing keys must appear in their original order with word boundaries, so a
	// reorder or a substring-preserving rename (e.g. status -> req_status) fails.
	assert.Regexp(t, `\bstatus=\S+ method=\S+ url=\S+ duration=\S+ bodySize=\S+`, out)
	assert.NotContains(t, out, "user_id=")
	assert.NotContains(t, out, "iss=")
}

// Fields set by a downstream handler must appear on the emitted line.
func TestLogging_EmitsSeededFields(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.FieldsFromContext(r.Context()).Set("user_id", "deadbeef")
		w.WriteHeader(http.StatusOK)
	})
	handler := Logging()(next)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/protocols", nil))

	assert.Contains(t, buf.String(), "user_id=deadbeef")
}

func TestLoggingAuth_AuthenticatedLineHasUserAndIss(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sub := hex.EncodeToString(pub)
	token := mintToken(t, priv, sub, "GET "+authTestPath, auth.MaxTokenLifetime, time.Now())

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := Logging()(Auth(auth.NewVerifier(), auth.Permissive, nil)(next))

	r := httptest.NewRequest(http.MethodGet, authTestPath, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(httptest.NewRecorder(), r)

	out := buf.String()
	assert.Contains(t, out, "user_id="+sub)
	assert.Contains(t, out, "iss=freighter-extension")
}

func TestLoggingAuth_AnonymousHasNoAuthFields(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := Logging()(Auth(auth.NewVerifier(), auth.Permissive, nil)(next))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, authTestPath, nil))

	out := buf.String()
	assert.NotContains(t, out, "user_id=")
	assert.NotContains(t, out, "iss=")
}

func TestTruncateForLog(t *testing.T) {
	assert.Equal(t, "freighter-extension", truncateForLog("freighter-extension"))
	long := strings.Repeat("a", 200)
	got := truncateForLog(long)
	assert.LessOrEqual(t, len(got), maxLoggedIssuerLen+len("…(truncated)"))
	assert.Contains(t, got, "(truncated)")
	// multi-byte safe: no invalid UTF-8 in the result
	assert.True(t, utf8.ValidString(got))
}

// The too_large rejection branch must log best-effort iss like the other
// rejection paths (IssuerFromRequestUnverified reads the header, which survives
// the oversized-body read that trips this branch).
func TestLoggingAuth_OversizedBodyLogsIss(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	const limit = 16
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sub := hex.EncodeToString(pub)
	token := mintToken(t, priv, sub, "POST "+authTestPath, auth.MaxTokenLifetime, time.Now())

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, authTestPath, bytes.NewReader(make([]byte, limit+1)))
	r.Header.Set("Authorization", "Bearer "+token)
	// Simulate the upstream BodySizeLimit middleware wrapping the body.
	r.Body = http.MaxBytesReader(rr, r.Body, limit)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := Logging()(Auth(auth.NewVerifier(), auth.Permissive, nil)(next))
	handler.ServeHTTP(rr, r)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	assert.Contains(t, buf.String(), "iss=freighter-extension")
}
