// ABOUTME: snake_case REST response types for GET /api/v1/accounts/{address}/transactions.
// ABOUTME: Mirrors the wallet-backend SDK types with consistent snake_case keys and string-encoded 64-bit ints.
package types

import "time"

// Transaction is the snake_case REST representation of a Stellar transaction.
// FeeCharged is string-encoded so JavaScript clients never lose precision and
// to match the "Stellar amounts as strings" convention used elsewhere.
type Transaction struct {
	Hash            string    `json:"hash"`
	FeeCharged      int64     `json:"fee_charged,string"`
	ResultCode      string    `json:"result_code"`
	LedgerNumber    uint32    `json:"ledger_number"`
	LedgerCreatedAt time.Time `json:"ledger_created_at"`
	IsFeeBump       bool      `json:"is_fee_bump"`
	IngestedAt      time.Time `json:"ingested_at"`
}

// Operation is the snake_case REST representation of a Stellar operation.
// ID is a TOID that routinely exceeds 2^53, so it is string-encoded to survive
// JSON parsing in JavaScript clients without precision loss.
type Operation struct {
	ID              int64     `json:"id,string"`
	OperationType   string    `json:"operation_type"`
	OperationXDR    string    `json:"operation_xdr"`
	ResultCode      string    `json:"result_code"`
	Successful      bool      `json:"successful"`
	LedgerNumber    uint32    `json:"ledger_number"`
	LedgerCreatedAt time.Time `json:"ledger_created_at"`
	IngestedAt      time.Time `json:"ingested_at"`
}

// StateChange is a sealed interface implemented by every state-change variant.
// The concrete type is determined by the StateChangeBase.Type discriminator
// (StateChangeCategory is 1:1 with the variants), so clients switch on "type".
type StateChange interface{ isStateChange() }

// StateChangeBase holds the fields common to every state-change variant.
type StateChangeBase struct {
	Type            string    `json:"type"`
	Reason          string    `json:"reason"`
	LedgerNumber    uint32    `json:"ledger_number"`
	LedgerCreatedAt time.Time `json:"ledger_created_at"`
	IngestedAt      time.Time `json:"ingested_at"`
}

func (StateChangeBase) isStateChange() {}

// StandardBalanceChange — category BALANCE.
type StandardBalanceChange struct {
	StateChangeBase
	StandardBalanceTokenID string `json:"standard_balance_token_id"`
	Amount                 string `json:"amount"`
}

// AccountChange — category ACCOUNT.
type AccountChange struct {
	StateChangeBase
	FunderAddress *string `json:"funder_address,omitempty"`
}

// SignerChange — category SIGNER.
type SignerChange struct {
	StateChangeBase
	SignerAddress *string `json:"signer_address,omitempty"`
	SignerWeights *string `json:"signer_weights,omitempty"`
}

// SignerThresholdsChange — category SIGNATURE_THRESHOLD.
type SignerThresholdsChange struct {
	StateChangeBase
	Thresholds string `json:"thresholds"`
}

// MetadataChange — category METADATA.
type MetadataChange struct {
	StateChangeBase
	MetadataKeyValue string `json:"metadata_key_value"`
}

// FlagsChange — category FLAGS.
type FlagsChange struct {
	StateChangeBase
	Flags []string `json:"flags"`
}

// TrustlineChange — category TRUSTLINE.
type TrustlineChange struct {
	StateChangeBase
	TrustlineTokenID         *string `json:"trustline_token_id,omitempty"`
	Limit                    *string `json:"limit,omitempty"`
	TrustlineLiquidityPoolID *string `json:"trustline_liquidity_pool_id,omitempty"`
}

// ReservesChange — category RESERVES.
type ReservesChange struct {
	StateChangeBase
	SponsoredAddress   *string `json:"sponsored_address,omitempty"`
	SponsorAddress     *string `json:"sponsor_address,omitempty"`
	SponsoredData      *string `json:"sponsored_data,omitempty"`
	SponsoredTrustline *string `json:"sponsored_trustline,omitempty"`
	ClaimableBalanceID *string `json:"claimable_balance_id,omitempty"`
	LiquidityPoolID    *string `json:"liquidity_pool_id,omitempty"`
}

// BalanceAuthorizationChange — category BALANCE_AUTHORIZATION.
type BalanceAuthorizationChange struct {
	StateChangeBase
	BalanceAuthTokenID         *string  `json:"balance_auth_token_id,omitempty"`
	BalanceAuthLiquidityPoolID *string  `json:"balance_auth_liquidity_pool_id,omitempty"`
	Flags                      []string `json:"flags"`
}

// AccountTransaction is one transaction plus the calling account's operations
// and state changes within it. The embedded Transaction's fields are promoted
// to the top level of the JSON object. Operations and StateChanges are always
// non-nil (empty slice, never null) when built by the service mapper.
type AccountTransaction struct {
	Transaction
	Operations   []Operation   `json:"operations"`
	StateChanges []StateChange `json:"state_changes"`
}
