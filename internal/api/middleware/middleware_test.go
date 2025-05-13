package middleware

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/utils"
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
	// Middleware are applied in reverse order of listing in Chain, so m2 runs "before" m1 wraps it.
	// But the actual execution flow when a request comes in is m1, then m2, then handler.
	// The test as originally written implies middleware2 should set the final header, which means
	// it's the "outer" middleware in terms of its effect being seen last if it acts before calling next.
	// Let's clarify: Chain(handler, m1, m2) -> m1(m2(handler)).
	// Request hits m1, m1 sets header, calls m2(handler).
	// Request hits m2, m2 sets header (overwrites m1), calls handler.
	assert.Equal(t, "middleware2-value", rec.Header().Get("X-Test-Header"))
}

func TestMiddleware_Logging(t *testing.T) {
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
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
}

func TestMiddleware_Recover(t *testing.T) {
	t.Parallel()

	t.Run("handler panics with a string", func(t *testing.T) {
		t.Parallel()
		mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("test panic with string")
		})
		recoverMiddleware := Recover()
		chainedHandler := recoverMiddleware(mockHandler)

		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		chainedHandler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "panic: test panic with string")
	})

	t.Run("handler panics with an actual error", func(t *testing.T) {
		t.Parallel()
		expectedErr := errors.New("panic with actual error")
		mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(expectedErr)
		})
		recoverMiddleware := Recover()
		chainedHandler := recoverMiddleware(mockHandler)

		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		chainedHandler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), expectedErr.Error())
	})

	t.Run("handler panics with ErrAbortHandler", func(t *testing.T) {
		t.Parallel()
		mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler)
		})
		recoverMiddleware := Recover()
		chainedHandler := recoverMiddleware(mockHandler)

		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()

		// Expect http.ErrAbortHandler to be re-panicked by the Recover middleware
		assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
			chainedHandler.ServeHTTP(rec, req)
		}, "Expected panic with http.ErrAbortHandler to be re-panicked")
	})

	t.Run("handler panics and response writing fails", func(t *testing.T) {
		t.Parallel()
		mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("test panic where response write also fails")
		})
		recoverMiddleware := Recover()
		chainedHandler := recoverMiddleware(mockHandler)

		req := httptest.NewRequest("GET", "/", nil)
		errorRec := utils.NewErrorResponseWriter(true)

		// We expect that the original panic about "response write also fails"
		// is written to the response, even if the Write to errorRec fails.
		// The http.Server will handle the low-level write error.
		// The Recover middleware should still attempt to write the panic.
		chainedHandler.ServeHTTP(errorRec, req)

		// The code would have been set to 500 by the Recover middleware before attempting to Write.
		assert.Equal(t, http.StatusInternalServerError, errorRec.Code)
	})

	t.Run("handler does not panic", func(t *testing.T) {
		t.Parallel()
		mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("ok"))
			require.NoError(t, err)
		})
		recoverMiddleware := Recover()
		chainedHandler := recoverMiddleware(mockHandler)

		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		chainedHandler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "ok", rec.Body.String())
	})
}
