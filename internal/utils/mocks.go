package utils

import (
	"context"

	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

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
	GetAccountTransactionsResult *types.PaginatedResponse[*wbtypes.GraphQLTransaction]
	// GetAccountTransactionsError is returned by GetAccountTransactions when
	// GetAccountTransactionsFunc is nil.
	GetAccountTransactionsError error
	// GetAccountTransactionsFunc overrides Result/Error when set; the test
	// controls the full response and can capture/assert call arguments.
	GetAccountTransactionsFunc func(ctx context.Context, address, network string, params types.AccountHistoryParams) (*types.PaginatedResponse[*wbtypes.GraphQLTransaction], error)

	// GetAccountOperationsResult is returned by GetAccountOperations when
	// GetAccountOperationsFunc is nil and GetAccountOperationsError is nil.
	GetAccountOperationsResult *types.PaginatedResponse[*wbtypes.Operation]
	// GetAccountOperationsError is returned by GetAccountOperations when
	// GetAccountOperationsFunc is nil.
	GetAccountOperationsError error
	// GetAccountOperationsFunc overrides Result/Error when set; the test
	// controls the full response and can capture/assert call arguments.
	GetAccountOperationsFunc func(ctx context.Context, address, network string, params types.AccountHistoryParams) (*types.PaginatedResponse[*wbtypes.Operation], error)

	// GetAccountStateChangesResult is returned by GetAccountStateChanges when
	// GetAccountStateChangesFunc is nil and GetAccountStateChangesError is nil.
	GetAccountStateChangesResult *types.PaginatedResponse[wbtypes.StateChangeNode]
	// GetAccountStateChangesError is returned by GetAccountStateChanges when
	// GetAccountStateChangesFunc is nil.
	GetAccountStateChangesError error
	// GetAccountStateChangesFunc overrides Result/Error when set; the test
	// controls the full response and can capture/assert call arguments.
	GetAccountStateChangesFunc func(ctx context.Context, address, network string, params types.AccountHistoryParams) (*types.PaginatedResponse[wbtypes.StateChangeNode], error)
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

// GetAccountTransactions returns a stubbed paginated transaction list. If
// GetAccountTransactionsFunc is set it takes precedence; otherwise
// GetAccountTransactionsError or GetAccountTransactionsResult is used.
func (m *MockWalletBackendService) GetAccountTransactions(ctx context.Context, address, network string, params types.AccountHistoryParams) (*types.PaginatedResponse[*wbtypes.GraphQLTransaction], error) {
	if m.GetAccountTransactionsFunc != nil {
		return m.GetAccountTransactionsFunc(ctx, address, network, params)
	}
	if m.GetAccountTransactionsError != nil {
		return nil, m.GetAccountTransactionsError
	}
	return m.GetAccountTransactionsResult, nil
}

// GetAccountOperations returns a stubbed paginated operations list. If
// GetAccountOperationsFunc is set it takes precedence; otherwise
// GetAccountOperationsError or GetAccountOperationsResult is used.
func (m *MockWalletBackendService) GetAccountOperations(ctx context.Context, address, network string, params types.AccountHistoryParams) (*types.PaginatedResponse[*wbtypes.Operation], error) {
	if m.GetAccountOperationsFunc != nil {
		return m.GetAccountOperationsFunc(ctx, address, network, params)
	}
	if m.GetAccountOperationsError != nil {
		return nil, m.GetAccountOperationsError
	}
	return m.GetAccountOperationsResult, nil
}

// GetAccountStateChanges returns a stubbed paginated state-changes list. If
// GetAccountStateChangesFunc is set it takes precedence; otherwise
// GetAccountStateChangesError or GetAccountStateChangesResult is used.
func (m *MockWalletBackendService) GetAccountStateChanges(ctx context.Context, address, network string, params types.AccountHistoryParams) (*types.PaginatedResponse[wbtypes.StateChangeNode], error) {
	if m.GetAccountStateChangesFunc != nil {
		return m.GetAccountStateChangesFunc(ctx, address, network, params)
	}
	if m.GetAccountStateChangesError != nil {
		return nil, m.GetAccountStateChangesError
	}
	return m.GetAccountStateChangesResult, nil
}
