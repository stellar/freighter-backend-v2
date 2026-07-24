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
// (the state-change category is 1:1 with the variants), so clients switch on
// "type".
type StateChange interface{ isStateChange() }

// StateChangeBase holds the fields common to every state-change variant. Type
// carries the state-change category (BALANCE, SIGNER, TRUSTLINE, ...).
type StateChangeBase struct {
	// Variant names the concrete state-change shape (the wallet-backend GraphQL
	// type, e.g. "BalanceChange", "FeeChange"). Type/reason alone do not identify
	// it: BalanceChange and FeeChange share (BALANCE, DEBIT/CREDIT), and account
	// creation and contract deployment share (ACCOUNT, CREATE).
	Variant         string    `json:"variant"`
	Type            string    `json:"type"`
	Reason          string    `json:"reason"`
	LedgerNumber    uint32    `json:"ledger_number"`
	LedgerCreatedAt time.Time `json:"ledger_created_at"`
	IngestedAt      time.Time `json:"ingested_at"`
}

func (StateChangeBase) isStateChange() {}

// BalanceChange — a movement of value on the account's token balance.
type BalanceChange struct {
	StateChangeBase
	TokenID   string  `json:"token_id"`
	Amount    string  `json:"amount"`
	ToMuxedID *string `json:"to_muxed_id,omitempty"`
}

// FeeChange — the transaction fee debited from (or refunded to) the account.
type FeeChange struct {
	StateChangeBase
	TokenID string `json:"token_id"`
	Amount  string `json:"amount"`
}

// AccountCreatedChange — a classic account creation.
type AccountCreatedChange struct {
	StateChangeBase
	FunderAddress string `json:"funder_address"`
}

// ContractDeployedChange — a smart-contract deployment.
type ContractDeployedChange struct {
	StateChangeBase
	DeployerAddress string `json:"deployer_address"`
}

// AccountMergedChange — an account merge.
type AccountMergedChange struct {
	StateChangeBase
	DestinationAddress string `json:"destination_address"`
}

// SignerAddedChange — a signer added to the account.
type SignerAddedChange struct {
	StateChangeBase
	SignerAddress string `json:"signer_address"`
	NewWeight     int32  `json:"new_weight"`
}

// SignerUpdatedChange — an existing signer's weight change.
type SignerUpdatedChange struct {
	StateChangeBase
	SignerAddress string `json:"signer_address"`
	OldWeight     *int32 `json:"old_weight,omitempty"`
	NewWeight     int32  `json:"new_weight"`
}

// SignerRemovedChange — a signer removed from the account.
type SignerRemovedChange struct {
	StateChangeBase
	SignerAddress string `json:"signer_address"`
	OldWeight     *int32 `json:"old_weight,omitempty"`
}

// ThresholdChange — a signature-threshold change; Reason identifies which one.
type ThresholdChange struct {
	StateChangeBase
	OldThreshold *int32 `json:"old_threshold,omitempty"`
	NewThreshold int32  `json:"new_threshold"`
}

// AccountFlagsChange — account authorization flags set or cleared.
type AccountFlagsChange struct {
	StateChangeBase
	Flags []string `json:"flags"`
}

// HomeDomainChange — a home-domain change on the account.
type HomeDomainChange struct {
	StateChangeBase
	OldHomeDomain *string `json:"old_home_domain,omitempty"`
	NewHomeDomain *string `json:"new_home_domain,omitempty"`
}

// DataEntryChange — a data entry created, updated, or removed on the account.
type DataEntryChange struct {
	StateChangeBase
	Name     string  `json:"name"`
	OldValue *string `json:"old_value,omitempty"`
	NewValue *string `json:"new_value,omitempty"`
}

// AllowanceChange — a SEP-41 allowance approval.
type AllowanceChange struct {
	StateChangeBase
	TokenID          string `json:"token_id"`
	Spender          string `json:"spender"`
	Amount           string `json:"amount"`
	ExpirationLedger uint32 `json:"expiration_ledger"`
}

// TrustlineAddedChange — a trustline created. Exactly one of TokenID /
// LiquidityPoolID is set.
type TrustlineAddedChange struct {
	StateChangeBase
	TokenID         *string `json:"token_id,omitempty"`
	LiquidityPoolID *string `json:"liquidity_pool_id,omitempty"`
	Limit           string  `json:"limit"`
}

// TrustlineUpdatedChange — a trustline limit update. Exactly one of TokenID /
// LiquidityPoolID is set.
type TrustlineUpdatedChange struct {
	StateChangeBase
	TokenID         *string `json:"token_id,omitempty"`
	LiquidityPoolID *string `json:"liquidity_pool_id,omitempty"`
	OldLimit        string  `json:"old_limit"`
	NewLimit        string  `json:"new_limit"`
}

// TrustlineRemovedChange — a trustline removed. Exactly one of TokenID /
// LiquidityPoolID is set.
type TrustlineRemovedChange struct {
	StateChangeBase
	TokenID         *string `json:"token_id,omitempty"`
	LiquidityPoolID *string `json:"liquidity_pool_id,omitempty"`
}

// SponsorshipChange — a base-reserve sponsorship established or released. At
// most one of the entity fields identifies what is sponsored.
type SponsorshipChange struct {
	StateChangeBase
	SponsoredAddress   *string `json:"sponsored_address,omitempty"`
	SponsorAddress     *string `json:"sponsor_address,omitempty"`
	TokenID            *string `json:"token_id,omitempty"`
	LiquidityPoolID    *string `json:"liquidity_pool_id,omitempty"`
	ClaimableBalanceID *string `json:"claimable_balance_id,omitempty"`
	DataName           *string `json:"data_name,omitempty"`
	SignerAddress      *string `json:"signer_address,omitempty"`
}

// BalanceAuthorizationChange — authorization to hold or transact an asset
// granted or revoked. Exactly one of TokenID / LiquidityPoolID is set; Flags is
// omitted for SAC contract-holder authorization, which has no trustline flags.
type BalanceAuthorizationChange struct {
	StateChangeBase
	TokenID         *string  `json:"token_id,omitempty"`
	LiquidityPoolID *string  `json:"liquidity_pool_id,omitempty"`
	Flags           []string `json:"flags,omitempty"`
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
