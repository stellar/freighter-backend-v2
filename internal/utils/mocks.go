package utils

import (
	"context"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/go-stellar-sdk/clients/rpcclient"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

type MockRPCService struct {
	SimulateError          error
	SimulateResultOverride *xdr.ScVal
	SimulatePanic          bool
	TokenURIOverride       string
	GetLedgerEntryOverride []types.LedgerEntryMap
	GetLedgerEntryError    error
	GetHealthFunc          func(network string) (types.GetHealthResponse, error)
}

func (m *MockRPCService) ConfigureNetworkClient(network string) *rpcclient.Client {
	return nil
}

func (m *MockRPCService) Name() string {
	return "mock-rpc"
}

func (m *MockRPCService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	if m.GetHealthFunc != nil {
		return m.GetHealthFunc(network)
	}
	return types.GetHealthResponse{Status: types.StatusHealthy}, nil
}

func (m *MockRPCService) SimulateTx(ctx context.Context, tx *txnbuild.Transaction, network string) (types.SimulateTransactionResponse, error) {
	return nil, nil
}

func (m *MockRPCService) SimulateInvocation(
	ctx context.Context,
	contractId xdr.ScAddress,
	sourceAccount *txnbuild.SimpleAccount,
	functionName xdr.ScSymbol,
	params []xdr.ScVal,
	timeout txnbuild.TimeBounds,
	network string,
) (types.SimulateTransactionResponse, error) {
	if m.SimulatePanic {
		panic("simulated RPC panic")
	}

	if m.SimulateError != nil {
		return nil, m.SimulateError
	}

	if m.SimulateResultOverride != nil {
		return m.SimulateResultOverride, nil
	}

	fn := string(functionName)

	var result xdr.ScVal
	switch fn {
	case "get_owner_tokens":
		scVec := xdr.ScVec{}
		vecPtr := &scVec
		result = xdr.ScVal{
			Type: xdr.ScValTypeScvVec,
			Vec:  &vecPtr,
		}
	case "owner_of":
		owner := xdr.ScSymbol("GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF")
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &owner}
	case "name":
		name := xdr.ScSymbol("MockNFT")
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &name}
	case "symbol":
		symbol := xdr.ScSymbol("MNFT")
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &symbol}
	case "token_uri":
		uriStr := "https://example.com/token.json"
		if m.TokenURIOverride != "" {
			uriStr = m.TokenURIOverride
		}
		uri := xdr.ScSymbol(uriStr)
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &uri}
	default:
		dummy := xdr.ScSymbol("dummy")
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &dummy}
	}

	return &result, nil
}

func (m *MockRPCService) GetLedgerEntries(ctx context.Context, keys []string, network string) ([]types.LedgerEntryMap, error) {
	if m.GetLedgerEntryOverride != nil {
		return m.GetLedgerEntryOverride, nil
	}

	if m.GetLedgerEntryError != nil {
		return nil, m.GetLedgerEntryError
	}
	return nil, nil
}

type MockWalletBackendService struct {
	GetBalancesOverride interface{}
	GetBalancesError    error

	// Result/Error are the default response when Func is nil. When Func is
	// set it takes precedence and the test owns the entire response shape —
	// useful for asserting that parsed query params were forwarded correctly.

	// GetAccountTransactionsResult is returned by GetAccountTransactions when
	// GetAccountTransactionsFunc is nil and GetAccountTransactionsError is nil.
	GetAccountTransactionsResult *types.PaginatedResponse[*types.AccountTransaction]
	// GetAccountTransactionsError is returned when GetAccountTransactionsFunc is nil.
	GetAccountTransactionsError error
	// GetAccountTransactionsFunc overrides Result/Error when set.
	GetAccountTransactionsFunc func(ctx context.Context, address, network string, params types.AccountHistoryParams) (*types.PaginatedResponse[*types.AccountTransaction], error)

	// Blend method stubs follow the same Result/Error/Func precedence.
	GetBlendPositionsResult *types.BlendAccountPositions
	GetBlendPositionsError  error
	GetBlendPositionsFunc   func(ctx context.Context, address, network string) (*types.BlendAccountPositions, error)

	GetBlendPoolsResult []types.BlendPool
	GetBlendPoolsError  error

	GetBlendEarnOptionsResult []types.BlendEarnOption
	GetBlendEarnOptionsError  error
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

// GetAccountTransactions returns a stubbed paginated transaction list.
func (m *MockWalletBackendService) GetAccountTransactions(ctx context.Context, address, network string, params types.AccountHistoryParams) (*types.PaginatedResponse[*types.AccountTransaction], error) {
	if m.GetAccountTransactionsFunc != nil {
		return m.GetAccountTransactionsFunc(ctx, address, network, params)
	}
	if m.GetAccountTransactionsError != nil {
		return nil, m.GetAccountTransactionsError
	}
	return m.GetAccountTransactionsResult, nil
}

func (m *MockWalletBackendService) GetBlendPositions(ctx context.Context, address, network string) (*types.BlendAccountPositions, error) {
	if m.GetBlendPositionsFunc != nil {
		return m.GetBlendPositionsFunc(ctx, address, network)
	}
	if m.GetBlendPositionsError != nil {
		return nil, m.GetBlendPositionsError
	}
	if m.GetBlendPositionsResult != nil {
		return m.GetBlendPositionsResult, nil
	}
	return &types.BlendAccountPositions{Pools: []types.BlendPoolPosition{}}, nil
}

func (m *MockWalletBackendService) GetBlendPools(ctx context.Context, network string) ([]types.BlendPool, error) {
	if m.GetBlendPoolsError != nil {
		return nil, m.GetBlendPoolsError
	}
	if m.GetBlendPoolsResult != nil {
		return m.GetBlendPoolsResult, nil
	}
	return []types.BlendPool{}, nil
}

func (m *MockWalletBackendService) GetBlendEarnOptions(ctx context.Context, network string) ([]types.BlendEarnOption, error) {
	if m.GetBlendEarnOptionsError != nil {
		return nil, m.GetBlendEarnOptionsError
	}
	if m.GetBlendEarnOptionsResult != nil {
		return m.GetBlendEarnOptionsResult, nil
	}
	return []types.BlendEarnOption{}, nil
}

type MockPricesService struct {
	GetPricesFunc     func(ctx context.Context, tokens []string, network string) (map[string]*types.PriceEntry, error)
	GetPricesOverride map[string]*types.PriceEntry
	GetPricesError    error
	LastTokens        []string
	LastNetwork       string
}

func (m *MockPricesService) Name() string { return "mock-prices" }

func (m *MockPricesService) GetPrices(ctx context.Context, tokens []string, network string) (map[string]*types.PriceEntry, error) {
	m.LastTokens = tokens
	m.LastNetwork = network
	if m.GetPricesFunc != nil {
		return m.GetPricesFunc(ctx, tokens, network)
	}
	if m.GetPricesError != nil {
		return nil, m.GetPricesError
	}
	if m.GetPricesOverride != nil {
		return m.GetPricesOverride, nil
	}
	return map[string]*types.PriceEntry{}, nil
}
