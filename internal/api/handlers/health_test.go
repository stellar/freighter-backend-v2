package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func TestHealthHandler_CheckHealth(t *testing.T) {
	t.Parallel()

	t.Run("should return healthy status", func(t *testing.T) {
		t.Parallel()
		handler := NewHealthHandler()

		req, _ := http.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()

		err := handler.CheckHealth(rr, req)
		require.NoError(t, err)

		// Check status code
		assert.Equal(t, http.StatusOK, rr.Code)

		// Check response headers
		assert.Equal(t, "no-cache, no-store, must-revalidate", rr.Header().Get("Cache-Control"))

		// Parse and check response body
		var response HealthResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, StatusHealthy, response.Status)
	})

	t.Run("should return error on write failure", func(t *testing.T) {
		t.Parallel()
		handler := NewHealthHandler()

		req, _ := http.NewRequest("GET", "/health", nil)
		w := utils.NewErrorResponseWriter(true)

		err := handler.CheckHealth(w, req)
		require.Error(t, err)
		httpErr, ok := err.(*httperror.HttpError)
		require.True(t, ok, "error should be an HttpError")
		assert.Equal(t, http.StatusInternalServerError, httpErr.StatusCode)
		assert.Contains(t, httpErr.Message, "writing health check response")
	})
}
