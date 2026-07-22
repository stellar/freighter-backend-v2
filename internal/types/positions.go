// ABOUTME: Response types for GET /api/v1/accounts/{address}/positions — the
// ABOUTME: frontend-shaped view of an account's DeFi positions (Blend only today).
package types

import "context"

// AccountPositions is the response body for the account positions endpoint.
// One payload powers both the Position Home screen (header + per-pool rows)
// and the Position Details screen (per-asset rows inside each pool): an
// account holds positions in at most a handful of pools, so full detail is
// always returned.
//
// Number conventions follow the wallet-backend upstream: USD/APY values are
// nullable JSON numbers where null means "unavailable" (no fresh oracle
// price), never zero; a genuinely zero value is 0. On-chain token amounts
// are full-precision integer strings in the asset's smallest unit (scale by
// Decimals for display).
type AccountPositions struct {
	// TotalValueUSD is the account's net position value across pools
	// (Σ pool NetUSD). Strict null propagation: if any pool's value is
	// unavailable the total is null rather than a silent undercount —
	// matching upstream's own convention for pool totals. 0 when the
	// account has no positions.
	TotalValueUSD *float64 `json:"total_value_usd"`
	// NetAPY is the NetUSD-weighted mean of the pools' net APYs; null when
	// any input is unavailable or the account has no priced value to weight.
	NetAPY *float64 `json:"net_apy"`
	// Positions has one row per (protocol, pool). Always non-nil; empty when
	// the account has no DeFi positions (including accounts unknown to the
	// indexer — indistinguishable by design).
	Positions []PoolPosition `json:"positions"`
}

// PoolPosition is one pool row. The common fields render a Position Home row
// for any protocol; protocol-specific detail lives under a key named after
// the protocol (only "blend" today), so adding a protocol later is additive.
type PoolPosition struct {
	Protocol string `json:"protocol"`
	// ID is the pool's contract address.
	ID string `json:"id"`
	// Name is the pool's display name; null when the upstream metadata
	// registry has no entry (clients fall back to a truncated ID).
	Name *string `json:"name"`
	// NetUSD is supplied minus borrowed for this pool.
	NetUSD      *float64 `json:"net_usd"`
	SuppliedUSD *float64 `json:"supplied_usd"`
	BorrowedUSD *float64 `json:"borrowed_usd"`
	// NetAPY is the account's net rate in this pool, as computed upstream.
	NetAPY *float64             `json:"net_apy"`
	Blend  *BlendPositionDetail `json:"blend,omitempty"`
}

// BlendPositionDetail is the Blend-specific detail for one pool. Reserve
// rows with no current balance on either side are filtered out (upstream
// emits fully-exited rows to carry realized-earnings history; the display
// list only shows live positions).
type BlendPositionDetail struct {
	// Supply has one row per asset the account deposits (plain supply and
	// supply-as-collateral combined; the split is preserved per row).
	Supply []BlendSupplyRow `json:"supply"`
	// Borrow has one row per asset the account owes.
	Borrow []BlendBorrowRow `json:"borrow"`
}

// BlendSupplyRow is one asset the account supplies in a pool.
type BlendSupplyRow struct {
	// AssetID is the asset's contract address.
	AssetID string `json:"asset_id"`
	// Symbol/Name/Decimals are nullable registry metadata.
	Symbol   *string `json:"symbol"`
	Name     *string `json:"name"`
	Decimals *int32  `json:"decimals"`
	// SuppliedTokens is the plain-supply portion, CollateralTokens the
	// portion posted as collateral; TotalTokens is their sum. All raw units.
	SuppliedTokens   string `json:"supplied_tokens"`
	CollateralTokens string `json:"collateral_tokens"`
	TotalTokens      string `json:"total_tokens"`
	// USDValue is the current USD value of TotalTokens.
	USDValue *float64 `json:"usd_value"`
	// APY is the current supply interest rate; EmissionsAPR is the BLND
	// emission rate on the supply side.
	APY          *float64 `json:"apy"`
	EmissionsAPR *float64 `json:"emissions_apr"`
	// InterestEarned is lifetime interest in raw token units (pure
	// interest: token-denominated upstream, so asset price movement never
	// contaminates it). InterestEarnedUSD converts it at today's price;
	// null when the price or decimals are unavailable.
	InterestEarned    string   `json:"interest_earned"`
	InterestEarnedUSD *float64 `json:"interest_earned_usd"`
	// ClaimableBLND is uncollected BLND emissions in raw units;
	// ClaimableUSD is its upstream-computed USD value.
	ClaimableBLND string   `json:"claimable_blnd"`
	ClaimableUSD  *float64 `json:"claimable_usd"`
	// PriceUSD is the pool oracle's per-unit price for this asset.
	PriceUSD *float64 `json:"price_usd"`
}

// BlendBorrowRow is one asset the account borrows in a pool.
type BlendBorrowRow struct {
	AssetID  string  `json:"asset_id"`
	Symbol   *string `json:"symbol"`
	Name     *string `json:"name"`
	Decimals *int32  `json:"decimals"`
	// BorrowedTokens is the debt in raw token units.
	BorrowedTokens string `json:"borrowed_tokens"`
	// USDValue is the current USD value of the debt.
	USDValue *float64 `json:"usd_value"`
	// APY is the current borrow interest rate.
	APY      *float64 `json:"apy"`
	PriceUSD *float64 `json:"price_usd"`
}

// PositionsService assembles the account positions view.
type PositionsService interface {
	Service
	GetAccountPositions(ctx context.Context, address, network string) (*AccountPositions, error)
}
