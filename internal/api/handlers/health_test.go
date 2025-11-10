package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func TestHealthHandler_CheckHealth(t *testing.T) {
	t.Parallel()

	t.Run("should return healthy status with healthy RPC", func(t *testing.T) {
		t.Parallel()
		mockRPC := &MockRPCService{
			HealthResponse: types.GetHealthResponse{
				Status: "healthy",
			},
		}
		handler := NewHealthHandler(mockRPC)

		req, _ := http.NewRequest("GET", "/health?network=PUBLIC", nil)
		rr := httptest.NewRecorder()

		err := handler.CheckHealth(rr, req)
		require.NoError(t, err)

		// Check response headers
		assert.Equal(t, "no-cache, no-store, must-revalidate", rr.Header().Get("Cache-Control"))

		// Parse and check response body
		var response HealthResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, "healthy", response.ServiceStatus["rpc"])
	})

	t.Run("should return 503 when RPC returns error", func(t *testing.T) {
		t.Parallel()
		mockRPC := &MockRPCService{
			HealthError: errors.New("rpc connection failed"),
			HealthResponse: types.GetHealthResponse{
				Status: types.StatusError,
			},
		}
		handler := NewHealthHandler(mockRPC)

		req, _ := http.NewRequest("GET", "/health?network=PUBLIC", nil)
		rr := httptest.NewRecorder()

		err := handler.CheckHealth(rr, req)
		require.NoError(t, err)

		// Check status code
		assert.Equal(t, http.StatusServiceUnavailable, rr.Code)

		// Parse and check response body
		var response HealthResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, types.StatusError, response.ServiceStatus["rpc"])
	})

	t.Run("should return 503 when RPC status is not healthy", func(t *testing.T) {
		t.Parallel()
		mockRPC := &MockRPCService{
			HealthResponse: types.GetHealthResponse{
				Status: "unhealthy",
			},
		}
		handler := NewHealthHandler(mockRPC)

		req, _ := http.NewRequest("GET", "/health?network=PUBLIC", nil)
		rr := httptest.NewRecorder()

		err := handler.CheckHealth(rr, req)
		require.NoError(t, err)

		// Check status code
		assert.Equal(t, http.StatusServiceUnavailable, rr.Code)

		// Parse and check response body
		var response HealthResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, "unhealthy", response.ServiceStatus["rpc"])
	})

	t.Run("should return error on write failure", func(t *testing.T) {
		t.Parallel()
		mockRPC := &MockRPCService{
			HealthResponse: types.GetHealthResponse{
				Status: "healthy",
			},
		}
		handler := NewHealthHandler(mockRPC)

		req, _ := http.NewRequest("GET", "/health?network=PUBLIC", nil)
		w := utils.NewErrorResponseWriter(true)

		err := handler.CheckHealth(w, req)
		require.Error(t, err)
		httpErr, ok := err.(*httperror.HttpError)
		require.True(t, ok, "error should be an HttpError")
		assert.Equal(t, http.StatusInternalServerError, httpErr.StatusCode)
		assert.Contains(t, httpErr.Message, "writing health check response")
	})
}
