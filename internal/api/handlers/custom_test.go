package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
)

// Define a struct to unmarshal JSON error responses for easier assertions
type jsonErrorResponse struct {
	Message    string                 `json:"message"`
	StatusCode int                    `json:"statusCode"`
	Extras     map[string]interface{} `json:"extras,omitempty"`
	// We don't need to assert OriginalError for these tests, so it can be omitted
}

func TestCustomHandler(t *testing.T) {
	t.Parallel()

	t.Run("handler returns no error", func(t *testing.T) {
		t.Parallel()
		mockHandler := func(w http.ResponseWriter, r *http.Request) error {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("Success"))
			return err
		}

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		handler := CustomHandler(mockHandler)
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "Success", rr.Body.String())
	})

	t.Run("handler returns a standard error", func(t *testing.T) {
		t.Parallel()
		mockHandler := func(w http.ResponseWriter, r *http.Request) error {
			return errors.New("standard error occurred")
		}

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		handler := CustomHandler(mockHandler)
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusInternalServerError, rr.Code)

		var errResp jsonErrorResponse
		err := json.Unmarshal(rr.Body.Bytes(), &errResp)
		require.NoError(t, err, "Failed to unmarshal error response")

		assert.Equal(t, "standard error occurred", errResp.Message)
		assert.Equal(t, http.StatusInternalServerError, errResp.StatusCode)
	})

	t.Run("handler returns an HttpError", func(t *testing.T) {
		t.Parallel()
		customHttpError := httperror.NewHttpError(
			"custom HTTP error",
			errors.New("underlying error"),
			http.StatusNotFound,
			map[string]interface{}{"detail": "resource not found"},
		)
		mockHandler := func(w http.ResponseWriter, r *http.Request) error {
			return customHttpError
		}

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		handler := CustomHandler(mockHandler)
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusNotFound, rr.Code)

		var errResp jsonErrorResponse
		err := json.Unmarshal(rr.Body.Bytes(), &errResp)
		require.NoError(t, err, "Failed to unmarshal error response")

		assert.Equal(t, "custom HTTP error", errResp.Message)
		assert.Equal(t, http.StatusNotFound, errResp.StatusCode)
		assert.NotNil(t, errResp.Extras)
		assert.Equal(t, "resource not found", errResp.Extras["detail"])
	})

	t.Run("handler writes to response writer and then returns error", func(t *testing.T) {
		t.Parallel()
		mockHandler := func(w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("X-Custom-Header", "test-value")
			w.WriteHeader(http.StatusAccepted)           // Write a status code
			_, err := w.Write([]byte("Partial content")) // Write some body
			require.NoError(t, err)
			return errors.New("error after partial write")
		}

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		handler := CustomHandler(mockHandler)
		handler.ServeHTTP(rr, req)

		// The important part is that the error is eventually rendered.
		// The status code might have been set before the error handler took over.
		assert.Equal(t, http.StatusAccepted, rr.Code)

		var errResp jsonErrorResponse
		// The body will contain "Partial content" followed by the JSON error.
		// We need to find the start of the JSON.
		bodyStr := rr.Body.String()
		jsonStartIndex := 0
		if idx := strings.Index(bodyStr, "{"); idx != -1 {
			jsonStartIndex = idx
		}
		err := json.Unmarshal([]byte(bodyStr[jsonStartIndex:]), &errResp)
		require.NoError(t, err, "Failed to unmarshal error response from body: %s", bodyStr)

		assert.Equal(t, "error after partial write", errResp.Message)
		// The HttpError created by CustomHandler for a standard error defaults to 500
		assert.Equal(t, http.StatusInternalServerError, errResp.StatusCode)
	})
}
