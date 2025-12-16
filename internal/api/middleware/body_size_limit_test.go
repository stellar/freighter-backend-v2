// ABOUTME: Unit tests for the body size limit middleware.
// ABOUTME: Tests various scenarios including body within limit, exceeding limit, and edge cases.
package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBodySizeLimit(t *testing.T) {
	t.Parallel()

	t.Run("allows requests within limit", func(t *testing.T) {
		t.Parallel()

		bodyContent := "test body content"
		var receivedBody string

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			receivedBody = string(body)
			w.WriteHeader(http.StatusOK)
		})

		middleware := BodySizeLimit(1024) // 1KB limit
		wrappedHandler := middleware(handler)

		req := httptest.NewRequest("POST", "/", strings.NewReader(bodyContent))
		rec := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, bodyContent, receivedBody)
	})

	t.Run("rejects requests exceeding limit", func(t *testing.T) {
		t.Parallel()

		// Create a body larger than the limit
		largeBody := bytes.Repeat([]byte("x"), 2048) // 2KB

		var readErr error
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, readErr = io.ReadAll(r.Body)
			if readErr != nil {
				http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
		})

		middleware := BodySizeLimit(1024) // 1KB limit
		wrappedHandler := middleware(handler)

		req := httptest.NewRequest("POST", "/", bytes.NewReader(largeBody))
		rec := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.NotNil(t, readErr)
		assert.True(t, IsMaxBytesError(readErr))
	})

	t.Run("uses default limit when zero is passed", func(t *testing.T) {
		t.Parallel()

		// Create a body larger than the default limit (1MB)
		largeBody := bytes.Repeat([]byte("x"), DefaultMaxBodySize+1)

		var readErr error
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, readErr = io.ReadAll(r.Body)
			if readErr != nil {
				http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
		})

		middleware := BodySizeLimit(0) // Should use default
		wrappedHandler := middleware(handler)

		req := httptest.NewRequest("POST", "/", bytes.NewReader(largeBody))
		rec := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.NotNil(t, readErr)
		assert.True(t, IsMaxBytesError(readErr))
	})

	t.Run("uses default limit when negative is passed", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		middleware := BodySizeLimit(-100) // Should use default
		wrappedHandler := middleware(handler)

		req := httptest.NewRequest("POST", "/", strings.NewReader("test"))
		rec := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("allows GET requests without body", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		middleware := BodySizeLimit(100)
		wrappedHandler := middleware(handler)

		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("allows exact limit size", func(t *testing.T) {
		t.Parallel()

		exactBody := bytes.Repeat([]byte("x"), 100)
		var receivedBody []byte

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			receivedBody = body
			w.WriteHeader(http.StatusOK)
		})

		middleware := BodySizeLimit(100)
		wrappedHandler := middleware(handler)

		req := httptest.NewRequest("POST", "/", bytes.NewReader(exactBody))
		rec := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, exactBody, receivedBody)
	})
}

func TestIsMaxBytesError(t *testing.T) {
	t.Parallel()

	t.Run("returns false for nil error", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsMaxBytesError(nil))
	})

	t.Run("returns false for other errors", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsMaxBytesError(io.EOF))
	})

	t.Run("returns true for MaxBytesError", func(t *testing.T) {
		t.Parallel()

		// Create a MaxBytesReader and force an error by reading more than limit
		body := strings.NewReader("this is a test body that exceeds the limit")
		limitedReader := http.MaxBytesReader(nil, io.NopCloser(body), 5)

		_, err := io.ReadAll(limitedReader)
		require.Error(t, err)
		assert.True(t, IsMaxBytesError(err))
	})
}
