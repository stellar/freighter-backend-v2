package types

import (
	"context"

	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
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
	SimulateTx(ctx context.Context, tx *txnbuild.Transaction) (SimulateTransactionResponse, error)
	InvokeContract(
		ctx context.Context,
		contractId xdr.ScAddress,
		sourceAccount *txnbuild.SimpleAccount,
		functionName xdr.ScSymbol,
		params []xdr.ScVal,
		timeout txnbuild.TimeBounds,
	) (SimulateTransactionResponse, error)
}
