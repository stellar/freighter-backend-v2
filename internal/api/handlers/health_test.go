package handlers

import (
	"context"
	"encoding/json"
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

	t.Run("should return healthy status when RPC is healthy for default network", func(t *testing.T) {
		t.Parallel()
		mockRPC := &utils.MockRPCService{}
		handler := NewHealthHandler(mockRPC)

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
		assert.Equal(t, types.StatusHealthy, response.RPCHealth.Status)
	})

	t.Run("should return healthy status for specified network", func(t *testing.T) {
		t.Parallel()
		mockRPC := &utils.MockRPCService{}
		handler := NewHealthHandler(mockRPC)

		req, _ := http.NewRequest("GET", "/health?network=TESTNET", nil)
		rr := httptest.NewRecorder()

		err := handler.CheckHealth(rr, req)
		require.NoError(t, err)

		// Check status code
		assert.Equal(t, http.StatusOK, rr.Code)

		// Parse and check response body
		var response HealthResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, StatusHealthy, response.Status)
		assert.Equal(t, types.StatusHealthy, response.RPCHealth.Status)
	})

	t.Run("should return healthy status even when RPC is unhealthy", func(t *testing.T) {
		t.Parallel()
		mockRPC := &MockUnhealthyRPCService{}
		handler := NewHealthHandler(mockRPC)

		req, _ := http.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()

		err := handler.CheckHealth(rr, req)
		require.NoError(t, err)

		// Check status code - should still be 200 OK
		assert.Equal(t, http.StatusOK, rr.Code)

		// Parse and check response body
		var response HealthResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		// Overall status is always healthy
		assert.Equal(t, StatusHealthy, response.Status)
		// But RPC health shows as unhealthy
		assert.Equal(t, types.StatusUnhealthy, response.RPCHealth.Status)
	})

	t.Run("should return error status in rpc_health when GetHealth returns error", func(t *testing.T) {
		t.Parallel()
		mockRPC := &MockErrorRPCService{}
		handler := NewHealthHandler(mockRPC)

		req, _ := http.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()

		err := handler.CheckHealth(rr, req)
		require.NoError(t, err)

		// Check status code - should still be 200 OK
		assert.Equal(t, http.StatusOK, rr.Code)

		// Parse and check response body
		var response HealthResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		// Overall status is always healthy
		assert.Equal(t, StatusHealthy, response.Status)
		// RPC health shows as error when GetHealth fails
		assert.Equal(t, types.StatusError, response.RPCHealth.Status)
	})

	t.Run("should return error for invalid network", func(t *testing.T) {
		t.Parallel()
		mockRPC := &utils.MockRPCService{}
		handler := NewHealthHandler(mockRPC)

		req, _ := http.NewRequest("GET", "/health?network=INVALID", nil)
		rr := httptest.NewRecorder()

		err := handler.CheckHealth(rr, req)
		require.Error(t, err)
		httpErr, ok := err.(*httperror.HttpError)
		require.True(t, ok, "error should be an HttpError")
		assert.Equal(t, http.StatusBadRequest, httpErr.StatusCode)
		assert.Contains(t, httpErr.Message, "invalid network parameter")
	})

	t.Run("should return error on write failure", func(t *testing.T) {
		t.Parallel()
		mockRPC := &utils.MockRPCService{}
		handler := NewHealthHandler(mockRPC)

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

// MockUnhealthyRPCService is a mock that returns unhealthy status
type MockUnhealthyRPCService struct {
	utils.MockRPCService
}

func (m *MockUnhealthyRPCService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	return types.GetHealthResponse{Status: types.StatusUnhealthy}, nil
}

// MockErrorRPCService is a mock that returns an error from GetHealth
type MockErrorRPCService struct {
	utils.MockRPCService
}

func (m *MockErrorRPCService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	return types.GetHealthResponse{}, assert.AnError
}
