package types

import (
	"context"

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
}

// StellarExpertAsset is the subset of the Stellar Expert /asset/{id} response
// we care about for pricing.
type StellarExpertAsset struct {
	Price   float64      `json:"price"`
	Price7d [][2]float64 `json:"price7d"`
}

type StellarExpertService interface {
	Service
	GetAsset(ctx context.Context, network, assetID string) (*StellarExpertAsset, error)
}

// PriceEntry is the per-token shape returned to the client. Numeric fields
// are JSON strings to match the legacy v1 BigNumber output;
// PercentagePriceChange24h is nullable when 24h history is unavailable.
type PriceEntry struct {
	CurrentPrice             string  `json:"currentPrice"`
	PercentagePriceChange24h *string `json:"percentagePriceChange24h"`
}

type PricesService interface {
	Service
	GetPrices(ctx context.Context, tokens []string, network string) (map[string]*PriceEntry, error)
}
