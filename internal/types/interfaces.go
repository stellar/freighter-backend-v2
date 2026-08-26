package types

import (
	"context"
	"encoding/json"
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
// we care about for pricing, price history, and token stats. A zero Price
// means unpriceable — either Stellar Expert omitted the `price` field (a
// known but illiquid asset; JSON absence decodes to 0) or reported a genuine
// 0. Callers map that to a null price rather than a "0" string.
type StellarExpertAsset struct {
	Price float64 `json:"price"`
	// Supply is the raw Stellar-scoped issued amount, scaled by Decimals.
	// Kept as json.Number because real supplies exceed float64's 2^53 exact-
	// integer range (XLM: 1054439020873472865) and must not be corrupted.
	// Empty when upstream omits the field.
	Supply json.Number `json:"supply"`
	// Decimals is the supply scale; nil when upstream omits it (default 7).
	Decimals *int `json:"decimals"`
	// Volume7d is the raw upstream 7-day volume. Its units are UNCONFIRMED
	// (~1e7 times larger than USD for classic assets); it must never be
	// compared against a USD threshold without an explicit, config-enabled
	// conversion.
	Volume7d float64 `json:"volume7d"`
	// Created is the asset's creation time (unix seconds); XLM reports 0.
	Created int64 `json:"created"`
	// Trustlines.Funded is the funded-trustline count ("Holders"); nil when
	// upstream omits it.
	Trustlines struct {
		Funded *int64 `json:"funded"`
	} `json:"trustlines"`
}

// StellarExpertCandle is one row of /asset/{id}/candles.
// Measured wire shape: [ts, open, high, low, close, quote_volume,
// base_volume, trades]. NOTE: the upstream API docs list low before high,
// but index 2 >= index 3 held in 96/96 sampled candles for both XLM and
// USDC — index 2 is the high and index 3 is the low. Anyone adding
// High()/Low() accessors must wire them from this measured order, not the
// docs.
type StellarExpertCandle [8]float64

func (c StellarExpertCandle) TS() int64      { return int64(c[0]) }
func (c StellarExpertCandle) Open() float64  { return c[1] }
func (c StellarExpertCandle) Close() float64 { return c[4] }

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

// PricePoint is one plotted chart point: T is the candle's bucket-start
// timestamp (unix seconds, as upstream returns it) and P the bucket's close,
// serialized as a JSON string for the clients' BigNumber parsers.
type PricePoint struct {
	T int64  `json:"t"`
	P string `json:"p"`
}

// PriceChange is the spot-anchored range delta (§4.2): absolute =
// spot − first plotted close; percent = the same, relative, ×100. Both are
// JSON strings.
type PriceChange struct {
	Absolute string `json:"absolute"`
	Percent  string `json:"percent"`
}

// TokenPriceHistory is the /token-price-history payload minus the echoed
// token key (the handler adds it). Field semantics are frozen by the §6.1
// wire contract:
//   - ResolutionSeconds is DERIVED from the returned timestamps, never
//     echoed from the request (upstream silently coarsens).
//   - LowVolume is true|false|null: null means the verdict's input (the
//     asset payload) was unavailable — never reported as false.
//   - Change is null when the coverage guard rejects the window.
//   - Points is always present ([] when there is no data — never null, and
//     no-data is a 200, never a 404).
type TokenPriceHistory struct {
	Range             string       `json:"range"`
	ResolutionSeconds int64        `json:"resolutionSeconds"`
	Currency          string       `json:"currency"`
	LowVolume         *bool        `json:"lowVolume"`
	Change            *PriceChange `json:"change"`
	Points            []PricePoint `json:"points"`
}

type PriceHistoryService interface {
	Service
	// GetPriceHistory returns the chart series for one canonical token id.
	// No data is (points: [], change: null), not an error; only transient
	// upstream/system failures return errors.
	GetPriceHistory(ctx context.Context, canonical, network, historyRange string) (*TokenPriceHistory, error)
}

// TokenStats is the /token-stats payload minus the echoed token key. It
// contains ONLY sourceable fields: anything absent from the upstream payload
// (or unsourceable as designed — Market Cap, FDV, Max Supply, volume rows
// pending unit confirmation) is omitted entirely, never emitted as null or
// zero.
type TokenStats struct {
	// SupplyOnStellar is the raw upstream supply scaled by decimals
	// (default 7 when absent) — the Stellar-scoped issued amount, which is
	// NOT a global circulating supply.
	SupplyOnStellar *string `json:"supplyOnStellar,omitempty"`
	// Holders is trustlines.funded.
	Holders *int64 `json:"holders,omitempty"`
}

type TokenStatsService interface {
	Service
	GetTokenStats(ctx context.Context, canonical, network string) (*TokenStats, error)
}
