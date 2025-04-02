package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddleware_Chain(t *testing.T) {
	t.Parallel()
	// Create a mock handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Create a mock middleware
	middleware1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test-Header", "middleware1-value")
			next.ServeHTTP(w, r)
		})
	}

	// Create a mock middleware
	middleware2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test-Header", "middleware2-value")
			next.ServeHTTP(w, r)
		})
	}

	// Chain the middlewares - middleware1(middleware2(handler))
	chain := Chain(handler, middleware1, middleware2)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	// The last middleware should have set the header value.
	assert.Equal(t, "middleware2-value", rec.Header().Get("X-Test-Header"))
}

func TestMiddleware_Logging(t *testing.T) {
	t.Parallel()

	// Capture stdout to check log output. This approach has limitations
	// because it relies on manipulating global state (os.Stdout) and
	// assumes the global logger hasn't been initialized elsewhere.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	loggingMiddleware := Logging()
	chain := loggingMiddleware(handler)

	req := httptest.NewRequest("GET", "/testpath", nil)
	rec := httptest.NewRecorder()

	chain.ServeHTTP(rec, req)

	// Restore logger and read output
	err := w.Close()
	require.NoError(t, err)
	os.Stdout = oldStdout // Restore the real stdout

	// Read captured output from the reader end of the pipe
	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)
	logOutput := buf.String()

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, logOutput, "msg=\"Request completed\"")
	assert.Contains(t, logOutput, "status=200")
	assert.Contains(t, logOutput, "method=GET")
	assert.Contains(t, logOutput, "url=/testpath")
	assert.Contains(t, logOutput, "duration=")
}

func TestMiddleware_ResponseHeader(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	responseHeaderMiddleware := ResponseHeader()
	chain := responseHeaderMiddleware(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}
