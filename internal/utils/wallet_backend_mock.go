package utils

import (
	"context"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

type MockWalletBackendService struct {
	GetBalancesOverride interface{}
	GetBalancesError    error
}

func (m *MockWalletBackendService) Name() string {
	return "mock-wallet-backend"
}

func (m *MockWalletBackendService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	return types.GetHealthResponse{Status: types.StatusHealthy}, nil
}

func (m *MockWalletBackendService) GetBalancesByAccountAddresses(ctx context.Context, addresses []string, network string) (interface{}, error) {
	if m.GetBalancesError != nil {
		return nil, m.GetBalancesError
	}

	if m.GetBalancesOverride != nil {
		return m.GetBalancesOverride, nil
	}

	return nil, nil
}
