// ABOUTME: Contains unit tests for the RPC service implementation.
// ABOUTME: Tests service creation, naming, and health check functionality.

package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

func TestNewRPCService(t *testing.T) {
	rpcURL := "http://localhost:8000"
	service := NewRPCService(rpcURL)

	require.NotNil(t, service)
	assert.IsType(t, &rpcService{}, service)

	// Verify the service has a client
	rpcSvc := service.(*rpcService)
	assert.NotNil(t, rpcSvc.client)
}

func TestRPCService_Name(t *testing.T) {
	service := NewRPCService("http://localhost:8000")

	name := service.Name()

	assert.Equal(t, "rpc", name)
}

func TestRPCService_GetHealth(t *testing.T) {
	t.Run("returns healthy status when RPC is available", func(t *testing.T) {
		// Create a test server that responds with a healthy status
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "POST", r.Method)

			// Respond with a successful RPC response
			response := `{"jsonrpc":"2.0","id":1,"result":{"status":"healthy"}}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, response)
		}))
		defer server.Close()

		service := NewRPCService(server.URL)

		response, err := service.GetHealth(context.Background())

		require.NoError(t, err)
		assert.Equal(t, "healthy", response.Status)
	})

	t.Run("returns error status when server returns error", func(t *testing.T) {
		// Create a test server that returns an error
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		service := NewRPCService(server.URL)

		response, err := service.GetHealth(context.Background())

		require.Error(t, err)
		assert.Equal(t, types.StatusError, response.Status)
	})

	t.Run("returns error status on network failure", func(t *testing.T) {
		// Use an invalid URL to simulate network error
		service := NewRPCService("http://localhost:99999")

		response, err := service.GetHealth(context.Background())

		require.Error(t, err)
		assert.Equal(t, types.StatusError, response.Status)
	})
}

