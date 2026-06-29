package handlers

import (
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

func TestRPCHealthHandler_CheckRPCHealth(t *testing.T) {
	t.Parallel()

	t.Run("should return healthy status", func(t *testing.T) {
		t.Parallel()
		mockRPC := &utils.MockRPCService{
			GetHealthFunc: func(network string) (types.GetHealthResponse, error) {
				return types.GetHealthResponse{Status: types.StatusHealthy}, nil
			},
		}
		handler := NewRPCHealthHandler(mockRPC)

		req, err := http.NewRequest("GET", "/rpc-health", nil)
		require.NoError(t, err)
		rr := httptest.NewRecorder()

		err = handler.CheckRPCHealth(rr, req)
		require.NoError(t, err)

		// Check status code
		assert.Equal(t, http.StatusOK, rr.Code)

		// Check response headers
		assert.Equal(t, "no-cache, no-store, must-revalidate", rr.Header().Get("Cache-Control"))

		// Parse and check response body
		var response types.GetHealthResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, types.StatusHealthy, response.Status)
	})

	t.Run("should use network query parameter", func(t *testing.T) {
		t.Parallel()
		capturedNetwork := ""
		mockRPC := &utils.MockRPCService{
			GetHealthFunc: func(network string) (types.GetHealthResponse, error) {
				capturedNetwork = network
				return types.GetHealthResponse{Status: types.StatusHealthy}, nil
			},
		}
		handler := NewRPCHealthHandler(mockRPC)

		req, err := http.NewRequest("GET", "/rpc-health?network=TESTNET", nil)
		require.NoError(t, err)
		rr := httptest.NewRecorder()

		err = handler.CheckRPCHealth(rr, req)
		require.NoError(t, err)
		assert.Equal(t, "TESTNET", capturedNetwork)
	})

	t.Run("should default to PUBLIC network", func(t *testing.T) {
		t.Parallel()
		capturedNetwork := ""
		mockRPC := &utils.MockRPCService{
			GetHealthFunc: func(network string) (types.GetHealthResponse, error) {
				capturedNetwork = network
				return types.GetHealthResponse{Status: types.StatusHealthy}, nil
			},
		}
		handler := NewRPCHealthHandler(mockRPC)

		req, err := http.NewRequest("GET", "/rpc-health", nil)
		require.NoError(t, err)
		rr := httptest.NewRecorder()

		err = handler.CheckRPCHealth(rr, req)
		require.NoError(t, err)
		assert.Equal(t, types.PUBLIC, capturedNetwork)
	})

	t.Run("should reject an invalid network without calling the RPC service", func(t *testing.T) {
		t.Parallel()
		called := false
		mockRPC := &utils.MockRPCService{
			GetHealthFunc: func(network string) (types.GetHealthResponse, error) {
				called = true
				return types.GetHealthResponse{Status: types.StatusHealthy}, nil
			},
		}
		handler := NewRPCHealthHandler(mockRPC)

		req, err := http.NewRequest("GET", "/rpc-health?network=ATTACK_0001", nil)
		require.NoError(t, err)
		rr := httptest.NewRecorder()

		err = handler.CheckRPCHealth(rr, req)
		require.Error(t, err)
		httpErr, ok := err.(*httperror.HttpError)
		require.True(t, ok, "error should be an HttpError")
		assert.Equal(t, http.StatusBadRequest, httpErr.StatusCode)
		assert.False(t, called, "RPC service must not be called for an invalid network")
	})

	t.Run("should return unhealthy status on RPC service failure", func(t *testing.T) {
		t.Parallel()
		mockRPC := &utils.MockRPCService{
			GetHealthFunc: func(network string) (types.GetHealthResponse, error) {
				return types.GetHealthResponse{}, assert.AnError
			},
		}
		handler := NewRPCHealthHandler(mockRPC)

		req, err := http.NewRequest("GET", "/rpc-health", nil)
		require.NoError(t, err)
		rr := httptest.NewRecorder()

		err = handler.CheckRPCHealth(rr, req)
		require.NoError(t, err)

		// Check status code is still 200 OK
		assert.Equal(t, http.StatusOK, rr.Code)

		// Parse and check response body has unhealthy status
		var response types.GetHealthResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, types.StatusUnhealthy, response.Status)
	})

	t.Run("should return error on write failure", func(t *testing.T) {
		t.Parallel()
		mockRPC := &utils.MockRPCService{
			GetHealthFunc: func(network string) (types.GetHealthResponse, error) {
				return types.GetHealthResponse{Status: types.StatusHealthy}, nil
			},
		}
		handler := NewRPCHealthHandler(mockRPC)

		req, err := http.NewRequest("GET", "/rpc-health", nil)
		require.NoError(t, err)
		w := utils.NewErrorResponseWriter(true)

		err = handler.CheckRPCHealth(w, req)
		require.Error(t, err)
		httpErr, ok := err.(*httperror.HttpError)
		require.True(t, ok, "error should be an HttpError")
		assert.Equal(t, http.StatusInternalServerError, httpErr.StatusCode)
		assert.Contains(t, httpErr.Message, "error writing RPC health check response")
	})
}
