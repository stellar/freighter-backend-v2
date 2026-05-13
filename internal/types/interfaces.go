package types

import (
	"context"

	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

const (
	StatusHealthy   = "healthy"
	StatusUnhealthy = "unhealthy"
	StatusError     = "error"
)

const (
	PUBLIC    = "PUBLIC"
	TESTNET   = "TESTNET"
	FUTURENET = "FUTURENET"
)

type Service interface {
	Name() string
	GetHealth(ctx context.Context, network string) (GetHealthResponse, error)
}

type RPCService interface {
	Service
	SimulateTx(ctx context.Context, tx *txnbuild.Transaction, network string) (SimulateTransactionResponse, error)
	SimulateInvocation(
		ctx context.Context,
		contractId xdr.ScAddress,
		sourceAccount *txnbuild.SimpleAccount,
		functionName xdr.ScSymbol,
		params []xdr.ScVal,
		timeout txnbuild.TimeBounds,
		network string,
	) (SimulateTransactionResponse, error)
	GetLedgerEntries(ctx context.Context, keys []string, network string) ([]LedgerEntryMap, error)
}

type WalletBackendService interface {
	Service
	GetBalancesByAccountAddresses(ctx context.Context, addresses []string, network string) (interface{}, error)
	GetAccountTransactions(ctx context.Context, address, network string, params AccountHistoryParams) (*PaginatedResponse[*wbtypes.GraphQLTransaction], error)
	GetAccountOperations(ctx context.Context, address, network string, params AccountHistoryParams) (*PaginatedResponse[*wbtypes.Operation], error)
	GetAccountStateChanges(ctx context.Context, address, network string, params AccountHistoryParams) (*PaginatedResponse[wbtypes.StateChangeNode], error)
}
