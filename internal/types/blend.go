// ABOUTME: Decode types for wallet-backend's Blend v2 GraphQL surface (positions
// ABOUTME: and pool catalog), mirroring blend.graphqls field for field.
package types

// Conventions, from the wallet-backend schema (blend.graphqls):
//   - USD/APY values are nullable Float: null means "uncomputable" (an oracle
//     price is missing or >24h stale — the pool contract itself rejects prices
//     past that age); a genuinely zero balance is 0, not null. Decoded as
//     *float64 and propagated as null, never rendered as 0.
//   - On-chain token amounts are non-null String at full precision. Passed
//     through verbatim; never float-parsed.
//   - tokenName/tokenSymbol/tokenDecimals come from the contract_tokens
//     metadata registry and are nullable; display falls back to a truncated
//     contract address.
//
// Backstop fields/types are deliberately not modeled (out of scope for v1);
// the query documents don't select them, so decoding never sees them.

// BlendAccountPositions is Account.blendPositions: one account's lending,
// collateral, and borrowing positions across every Blend v2 pool it touched.
type BlendAccountPositions struct {
	Pools []BlendPoolPosition `json:"pools"`
}

// BlendPoolPosition rolls up an account's reserve positions within one pool.
// USDValue is supplied minus borrowed. NetAPY nets supply earnings against
// borrow interest over TOTAL SUPPLIED USD — the blend-sdk-js convention the
// Blend UI shows: (Σ supplied·supplyApy − Σ borrowed·borrowApy) / Σ supplied;
// 0 for a debt-only position, null when any reserve lacks a fresh price.
type BlendPoolPosition struct {
	PoolAddress string                 `json:"poolAddress"`
	PoolName    *string                `json:"poolName"`
	USDValue    *float64               `json:"usdValue"`
	SuppliedUSD *float64               `json:"suppliedUsd"`
	BorrowedUSD *float64               `json:"borrowedUsd"`
	NetAPY      *float64               `json:"netApy"`
	Reserves    []BlendReservePosition `json:"reserves"`
}

// BlendReservePosition is an account's position in one reserve of a pool.
// Token amounts are underlying-asset amounts at rates projected to now.
// InterestEarned is lifetime interest in underlying tokens (survives full
// exit — a zero-balance row still carries realized earnings; liquidations
// adjust the basis so the figure stays interest-only). EmissionsEarnedBLND
// is claimable (uncollected) BLND across the reserve's emission streams.
type BlendReservePosition struct {
	AssetContractID  string   `json:"assetContractId"`
	TokenName        *string  `json:"tokenName"`
	TokenSymbol      *string  `json:"tokenSymbol"`
	TokenDecimals    *int32   `json:"tokenDecimals"`
	SuppliedTokens   string   `json:"suppliedTokens"`
	CollateralTokens string   `json:"collateralTokens"`
	BorrowedTokens   string   `json:"borrowedTokens"`
	SuppliedUSD      *float64 `json:"suppliedUsd"`
	BorrowedUSD      *float64 `json:"borrowedUsd"`
	SupplyAPY        *float64 `json:"supplyApy"`
	BorrowAPY        *float64 `json:"borrowApy"`
	// EmissionsSupplyAPR / EmissionsBorrowAPR are the reserve's POOL-WIDE
	// per-side emission-stream APRs (not scaled to this account's holding):
	// 0 when the side has no active stream, null when the stream is active
	// but a price is unavailable.
	EmissionsSupplyAPR  *float64 `json:"emissionsSupplyApr"`
	EmissionsBorrowAPR  *float64 `json:"emissionsBorrowApr"`
	InterestEarned      string   `json:"interestEarned"`
	EmissionsEarnedBLND string   `json:"emissionsEarnedBlnd"`
	EmissionsEarnedUSD  *float64 `json:"emissionsEarnedUsd"`
	PriceUSD            *float64 `json:"priceUsd"`
}

// BlendPool is one pool in the pool-wide catalog (Query.blendPools),
// independent of any account. SuppliedUSD/BorrowedUSD are strict-null: a
// missing price on any reserve makes the pool total uncomputable.
// InterestAPY is the supplied-USD-weighted supply rate (interest only);
// NetAPY additionally folds in BLND emissions — supply-side yield, not
// netted against borrows.
type BlendPool struct {
	Address     string         `json:"address"`
	Name        *string        `json:"name"`
	Status      *string        `json:"status"`
	SuppliedUSD *float64       `json:"suppliedUsd"`
	BorrowedUSD *float64       `json:"borrowedUsd"`
	InterestAPY *float64       `json:"interestApy"`
	NetAPY      *float64       `json:"netApy"`
	Reserves    []BlendReserve `json:"reserves"`
}

// BlendPoolStatus enum values (BlendPool.Status). The first four accept
// supply (deposits); the first two also allow borrowing; the rest reject
// both. Status is null until the pool's config entry has been ingested.
const (
	BlendPoolStatusAdminActive = "ADMIN_ACTIVE"
	BlendPoolStatusActive      = "ACTIVE"
	BlendPoolStatusAdminOnIce  = "ADMIN_ON_ICE"
	BlendPoolStatusOnIce       = "ON_ICE"
	BlendPoolStatusAdminFrozen = "ADMIN_FROZEN"
	BlendPoolStatusFrozen      = "FROZEN"
	BlendPoolStatusSetup       = "SETUP"
)

// BlendReserve is a pool-wide reserve catalog row: rates and totals as of
// now, no per-account data.
type BlendReserve struct {
	AssetContractID    string   `json:"assetContractId"`
	TokenName          *string  `json:"tokenName"`
	TokenSymbol        *string  `json:"tokenSymbol"`
	TokenDecimals      *int32   `json:"tokenDecimals"`
	Enabled            bool     `json:"enabled"`
	Utilization        *float64 `json:"utilization"`
	SupplyAPY          *float64 `json:"supplyApy"`
	BorrowAPY          *float64 `json:"borrowApy"`
	EmissionsSupplyAPR *float64 `json:"emissionsSupplyApr"`
	SuppliedUSD        *float64 `json:"suppliedUsd"`
	BorrowedUSD        *float64 `json:"borrowedUsd"`
	PriceUSD           *float64 `json:"priceUsd"`
}
