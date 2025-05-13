package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/utils" // Import shared test utils
)

func TestHealthCheckHandler(t *testing.T) {
	t.Parallel()

	t.Run("should return 200 OK", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()
		err := HealthCheckHandler(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "OK", rr.Body.String())
	})

	t.Run("should return error on write failure", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest("GET", "/health", nil)
		w := utils.NewErrorResponseWriter(true)
		err := HealthCheckHandler(w, req)
		require.Error(t, err)
		httpErr, ok := err.(*httperror.HttpError)
		require.True(t, ok, "error should be an HttpError")
		assert.Equal(t, http.StatusInternalServerError, httpErr.StatusCode)
		assert.Contains(t, httpErr.Message, "writing health check response")
		assert.Contains(t, httpErr.Error(), "simulated writer error")
	})
}
