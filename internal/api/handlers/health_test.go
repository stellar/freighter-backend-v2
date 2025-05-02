package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthCheckHandler(t *testing.T) {
	t.Run("should return 200 status code", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest("GET", "/api/v1/health", nil)
		rr := httptest.NewRecorder()
		err := HealthCheckHandler(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)
	})
	t.Run("should return error if writing response fails", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest("GET", "/api/v1/health", nil)
		rr := newErrorResponseWriter()
		err := HealthCheckHandler(rr, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "writing health check response")
	})
}
