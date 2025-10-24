package types

import (
	"context"

	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
	"github.com/stellar/stellar-rpc/client"
)

const (
	StatusHealthy   = "healthy"
	StatusUnhealthy = "unhealthy"
	StatusError     = "error"
)

type Service interface {
	Name() string
	GetHealth(ctx context.Context) (GetHealthResponse, error)
}

type RPCService interface {
	Service
	ConfigureNetworkClient(network string) *client.Client
	SimulateTx(ctx context.Context, tx *txnbuild.Transaction) (SimulateTransactionResponse, error)
	SimulateInvocation(
		ctx context.Context,
		contractId xdr.ScAddress,
		sourceAccount *txnbuild.SimpleAccount,
		functionName xdr.ScSymbol,
		params []xdr.ScVal,
		timeout txnbuild.TimeBounds,
	) (SimulateTransactionResponse, error)
	GetLedgerEntry(ctx context.Context, keys []string, network string) ([]LedgerEntryMap, error)
}

const (
	PUBLIC = "PUBLIC"
	TESTNET = "TESTNET"
	FUTURENET = "FUTURENET"
)
