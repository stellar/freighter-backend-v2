package handlers

import (
	"context"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// MockRPCService mocks the RPCService interface for testing
type MockRPCService struct {
	HealthResponse types.GetHealthResponse
	HealthError    error
}

func (m *MockRPCService) GetHealth(ctx context.Context) (types.GetHealthResponse, error) {
	return m.HealthResponse, m.HealthError
}

func (m *MockRPCService) Name() string {
	return "rpc"
}
