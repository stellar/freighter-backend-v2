package types

import (
	"context"
	"time"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

const (
	StatusHealthy   = "healthy"
	StatusUnhealthy = "unhealthy"
	StatusError     = "error"
	// StatusDisabled is reported by db-health when the database is turned off
	// (DB_ENABLED=false / no pool opened), so probes can tell "off on purpose"
	// apart from "configured but unreachable" (StatusUnhealthy).
	StatusDisabled = "disabled"
)

const (
	PUBLIC    = "PUBLIC"
	TESTNET   = "TESTNET"
	FUTURENET = "FUTURENET"
)

type Service interface {
	Name() string
}

type RPCService interface {
	Service
	GetHealth(ctx context.Context, network string) (GetHealthResponse, error)
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
	GetHealth(ctx context.Context, network string) (GetHealthResponse, error)
	GetBalancesByAccountAddresses(ctx context.Context, addresses []string, network string) (interface{}, error)
	GetAccountTransactions(ctx context.Context, address, network string, params AccountHistoryParams) (*PaginatedResponse[*AccountTransaction], error)
}

// StellarExpertAsset is the subset of the Stellar Expert /asset/{id} response
// we care about for pricing. A zero Price means unpriceable — either Stellar
// Expert omitted the `price` field (a known but illiquid asset; JSON absence
// decodes to 0) or reported a genuine 0. Callers map that to a null price
// rather than a "0" string.
type StellarExpertAsset struct {
	Price float64 `json:"price"`
}

// StellarExpertCandle is one row of /asset/{id}/candles.
// Wire shape per Stellar Expert API docs: [ts, open, low, high, close,
// quote_volume, base_volume, trades].
type StellarExpertCandle [8]float64

func (c StellarExpertCandle) TS() int64     { return int64(c[0]) }
func (c StellarExpertCandle) Open() float64 { return c[1] }

type StellarExpertService interface {
	Service
	GetAsset(ctx context.Context, network, assetID string) (*StellarExpertAsset, error)
	GetAssetCandles(ctx context.Context, network, assetID string, from, to time.Time, resolutionSec int) ([]StellarExpertCandle, error)
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
