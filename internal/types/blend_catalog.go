// ABOUTME: Response types for the Blend market-catalog endpoints:
// ABOUTME: GET /protocols/blend/pools and GET /protocols/blend/earn-options.
package types

import "context"

// BlendPoolsCatalog is the response body for the pools endpoint: the
// pool-wide market view (no account data), serving the Pool Details screen.
// Number conventions match the positions endpoint: USD/APY are nullable
// JSON numbers (null = no fresh oracle price), token metadata is nullable
// registry data.
type BlendPoolsCatalog struct {
	// Pools is every Blend pool known to the indexer, unfiltered. Always
	// non-nil.
	Pools []BlendCatalogPool `json:"pools"`
}

// BlendCatalogPool is one pool in the market catalog.
type BlendCatalogPool struct {
	// ID is the pool's contract address.
	ID string `json:"id"`
	// Name is null when the metadata registry has no entry.
	Name *string `json:"name"`
	// Status is the raw on-chain pool status (0 Admin Active, 1 Active,
	// 2 Admin On-Ice, 3 On-Ice, 4 Admin Frozen, 5 Frozen, 6 Setup;
	// 0-3 accept deposits, 0-1 also allow borrowing). Null until the pool's
	// config has been ingested.
	Status *int32 `json:"status"`
	// SuppliedUSD/BorrowedUSD are pool-wide totals with strict null
	// propagation upstream: one unpriced reserve nulls the pool total.
	SuppliedUSD *float64 `json:"supplied_usd"`
	BorrowedUSD *float64 `json:"borrowed_usd"`
	// InterestAPY is the supplied-USD-weighted supply rate (interest only).
	// NetAPY additionally includes BLND emissions; it is a supply-side
	// yield, not netted against the pool's borrow side.
	InterestAPY *float64 `json:"interest_apy"`
	NetAPY      *float64 `json:"net_apy"`
	// Reserves lists the pool's assets with current market rates.
	Reserves []BlendCatalogReserve `json:"reserves"`
}

// BlendCatalogReserve is one (pool, asset) market row.
type BlendCatalogReserve struct {
	AssetID  string  `json:"asset_id"`
	Symbol   *string `json:"symbol"`
	Name     *string `json:"name"`
	Decimals *int32  `json:"decimals"`
	// Enabled is the reserve's own on/off flag, independent of pool status.
	Enabled bool `json:"enabled"`
	// Utilization is borrowed/supplied, clamped at 100% upstream.
	Utilization *float64 `json:"utilization"`
	SupplyAPY   *float64 `json:"supply_apy"`
	BorrowAPY   *float64 `json:"borrow_apy"`
	// EmissionsSupplyAPR is the BLND emission rate on the supply side.
	EmissionsSupplyAPR *float64 `json:"emissions_supply_apr"`
	SuppliedUSD        *float64 `json:"supplied_usd"`
	BorrowedUSD        *float64 `json:"borrowed_usd"`
	PriceUSD           *float64 `json:"price_usd"`
}

// BlendEarnOptionsCatalog is the response body for the earn-options
// endpoint: "where can I earn this asset", serving the Earn select-token and
// select-pool screens. Upstream already excludes disabled reserves and
// pools that reject deposits; freighter additionally filters pools through
// the operator-curated allowlist when one is configured.
type BlendEarnOptionsCatalog struct {
	// Options has one entry per earnable asset. Always non-nil; assets whose
	// every pool was removed by the allowlist are dropped.
	Options []BlendEarnAssetOption `json:"options"`
}

// BlendEarnAssetOption is one earnable asset and the pools offering it.
type BlendEarnAssetOption struct {
	AssetID  string  `json:"asset_id"`
	Symbol   *string `json:"symbol"`
	Name     *string `json:"name"`
	Decimals *int32  `json:"decimals"`
	// Pools is ordered by upstream (supplied USD descending). The
	// emissions-inclusive earn headline is SupplyAPY + EmissionsSupplyAPR.
	Pools []BlendEarnPool `json:"pools"`
}

// BlendEarnPool is one pool's offer for an asset.
type BlendEarnPool struct {
	ID                 string   `json:"id"`
	Name               *string  `json:"name"`
	SupplyAPY          *float64 `json:"supply_apy"`
	EmissionsSupplyAPR *float64 `json:"emissions_supply_apr"`
	SuppliedUSD        *float64 `json:"supplied_usd"`
}

// BlendCatalogService serves the address-independent Blend market views.
type BlendCatalogService interface {
	Service
	GetPools(ctx context.Context, network string) (*BlendPoolsCatalog, error)
	GetEarnOptions(ctx context.Context, network string) (*BlendEarnOptionsCatalog, error)
}
