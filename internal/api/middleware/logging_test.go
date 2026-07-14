package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

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
	for _, key := range []string{"status=", "method=", "url=", "duration=", "bodySize="} {
		assert.Contains(t, out, key, "existing key %q must remain", key)
	}
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
