// ABOUTME: snake_case REST response types for the account balances endpoint.
// ABOUTME: Mirrors the wallet-backend SDK balance variants with consistent snake_case keys; `available` is the spendable amount.
package types

// Balance is a sealed interface implemented by every balance variant. The
// concrete type is determined by the BalanceBase.TokenType discriminator
// (token_type is 1:1 with the variants), so clients switch on "token_type".
type Balance interface{ isBalance() }

// BalanceBase holds the fields common to every balance variant. Balance is the
// on-ledger amount and Available is the spendable portion (balance minus the
// reserved amount for native/classic; equal to balance for contract tokens and
// pool shares). Both are Stellar amount strings so JavaScript clients never
// lose precision.
type BalanceBase struct {
	Balance   string `json:"balance"`
	Available string `json:"available"`
	TokenID   string `json:"token_id"`
	TokenType string `json:"token_type"`
}

func (BalanceBase) isBalance() {}

// NativeBalance — token_type NATIVE.
type NativeBalance struct {
	BalanceBase
	MinimumBalance     string `json:"minimum_balance"`
	BuyingLiabilities  string `json:"buying_liabilities"`
	SellingLiabilities string `json:"selling_liabilities"`
	LastModifiedLedger uint32 `json:"last_modified_ledger"`
}

// TrustlineBalance — token_type CLASSIC. Code and Issuer are omitted for the
// native asset (which never appears as a trustline, but the SDK models them as
// optional).
type TrustlineBalance struct {
	BalanceBase
	Code                              *string `json:"code,omitempty"`
	Issuer                            *string `json:"issuer,omitempty"`
	Type                              string  `json:"type"`
	Limit                             string  `json:"limit"`
	BuyingLiabilities                 string  `json:"buying_liabilities"`
	SellingLiabilities                string  `json:"selling_liabilities"`
	LastModifiedLedger                uint32  `json:"last_modified_ledger"`
	IsAuthorized                      bool    `json:"is_authorized"`
	IsAuthorizedToMaintainLiabilities bool    `json:"is_authorized_to_maintain_liabilities"`
}

// SACBalance — token_type SAC. Symbol/name are intentionally omitted; the
// client derives them from the code/issuer asset.
type SACBalance struct {
	BalanceBase
	Code              string `json:"code"`
	Issuer            string `json:"issuer"`
	Decimals          int32  `json:"decimals"`
	IsAuthorized      bool   `json:"is_authorized"`
	IsClawbackEnabled bool   `json:"is_clawback_enabled"`
}

// SEP41Balance — token_type SEP41. Balance is the raw i128 amount as a
// decimal string, not scaled by Decimals.
type SEP41Balance struct {
	BalanceBase
	Symbol             *string `json:"symbol,omitempty"`
	Name               *string `json:"name,omitempty"`
	Decimals           int32   `json:"decimals"`
	LastModifiedLedger uint32  `json:"last_modified_ledger"`
}

// LiquidityPoolReserve is one constituent asset of a liquidity pool and its
// reserve amount.
type LiquidityPoolReserve struct {
	Asset  string `json:"asset"`
	Amount string `json:"amount"`
}

// LiquidityPoolBalance — token_type LIQUIDITY_POOL. Balance is the account's
// pool shares; Reserves carries the pool's constituent assets and amounts.
type LiquidityPoolBalance struct {
	BalanceBase
	LiquidityPoolID    string                 `json:"liquidity_pool_id"`
	Reserves           []LiquidityPoolReserve `json:"reserves"`
	LastModifiedLedger uint32                 `json:"last_modified_ledger"`
}
