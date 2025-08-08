package types

import (
	"context"

	"github.com/stellar/go/txnbuild"
	"github.com/stellar/stellar-rpc/client"
)

const (
	StatusHealthy   = "healthy"
	StatusUnhealthy = "unhealthy"
	StatusError     = "error"
)

type RPCService interface {
	GetHealth(ctx context.Context) (GetHealthResponse, error)
	SimulateTx(ctx context.Context, rpc *client.Client, tx *txnbuild.Transaction) (GetHealthResponse, error)
}

type Service interface {
	Name() string
	GetHealth(ctx context.Context) (GetHealthResponse, error)
	SimulateTx(ctx context.Context, tx *txnbuild.Transaction) (SimulateTransactionResponse, error)
}
