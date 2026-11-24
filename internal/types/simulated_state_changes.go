// ABOUTME: snake_case REST response types for POST /api/v1/simulate-state-changes.
// ABOUTME: Simulated state changes mirror the history variants, minus the ledger fields a preview does not have.
package types

import "fmt"

// Codes wallet-backend returns for a transaction it cannot preview.
const (
	SimulationCodeInvalidTransactionXDR  = "INVALID_TRANSACTION_XDR"
	SimulationCodeUnsupportedTransaction = "UNSUPPORTED_TRANSACTION"
	SimulationCodeSimulationFailed       = "SIMULATION_FAILED"
)

// SimulationRejectedError reports a transaction wallet-backend could not
// preview. It is a normal client outcome, not an upstream failure.
type SimulationRejectedError struct {
	Code    string
	Message string
}

func (e *SimulationRejectedError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// SimulatedStateChanges is the preview of an unsubmitted transaction.
// StateChanges and Tokens are always non-nil.
type SimulatedStateChanges struct {
	LatestLedger uint32                 `json:"latest_ledger"`
	StateChanges []SimulatedStateChange `json:"state_changes"`
	Tokens       []SimulatedToken       `json:"tokens"`
}

// SimulatedToken is display metadata for a token the state changes reference.
// A token wallet-backend has never indexed has no entry.
type SimulatedToken struct {
	TokenID  string  `json:"token_id"`
	Code     *string `json:"code,omitempty"`
	Issuer   *string `json:"issuer,omitempty"`
	Name     *string `json:"name,omitempty"`
	Symbol   *string `json:"symbol,omitempty"`
	Decimals uint32  `json:"decimals"`
}

// SimulatedStateChange is a sealed interface implemented by every simulated
// state-change variant.
type SimulatedStateChange interface{ isSimulatedStateChange() }

// SimulatedStateChangeBase holds the fields common to every simulated variant.
// Variant uses the same names as the history endpoint ("BalanceChange", ...),
// so clients share one switch. OperationIndex is the zero-based index of the
// operation that would produce the change. It is always set: the fee row, the
// only row without one, is dropped before the response is built.
type SimulatedStateChangeBase struct {
	Variant        string `json:"variant"`
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	AccountAddress string `json:"account_address"`
	OperationIndex *int32 `json:"operation_index"`
}

func (SimulatedStateChangeBase) isSimulatedStateChange() {}

// The variants below carry the same fields and JSON keys as their history
// counterparts in account_history.go.

type SimulatedBalanceChange struct {
	SimulatedStateChangeBase
	TokenID   string  `json:"token_id"`
	Amount    string  `json:"amount"`
	ToMuxedID *string `json:"to_muxed_id,omitempty"`
}

type SimulatedAccountCreatedChange struct {
	SimulatedStateChangeBase
	CreatorAddress string `json:"creator_address"`
}

type SimulatedAccountMergedChange struct {
	SimulatedStateChangeBase
	DestinationAddress string `json:"destination_address"`
}

type SimulatedSignerAddedChange struct {
	SimulatedStateChangeBase
	SignerAddress string `json:"signer_address"`
	NewWeight     int32  `json:"new_weight"`
}

type SimulatedSignerUpdatedChange struct {
	SimulatedStateChangeBase
	SignerAddress string `json:"signer_address"`
	OldWeight     int32  `json:"old_weight"`
	NewWeight     int32  `json:"new_weight"`
}

type SimulatedSignerRemovedChange struct {
	SimulatedStateChangeBase
	SignerAddress string `json:"signer_address"`
	OldWeight     int32  `json:"old_weight"`
}

type SimulatedThresholdChange struct {
	SimulatedStateChangeBase
	Threshold    string `json:"threshold"`
	OldThreshold int32  `json:"old_threshold"`
	NewThreshold int32  `json:"new_threshold"`
}

type SimulatedAccountFlagsChange struct {
	SimulatedStateChangeBase
	Flags []string `json:"flags"`
}

type SimulatedHomeDomainSetChange struct {
	SimulatedStateChangeBase
	HomeDomain string `json:"home_domain"`
}

type SimulatedHomeDomainUpdatedChange struct {
	SimulatedStateChangeBase
	OldHomeDomain string `json:"old_home_domain"`
	NewHomeDomain string `json:"new_home_domain"`
}

type SimulatedHomeDomainClearedChange struct {
	SimulatedStateChangeBase
	OldHomeDomain string `json:"old_home_domain"`
}

type SimulatedDataEntryAddedChange struct {
	SimulatedStateChangeBase
	Name  string `json:"name"`
	Value string `json:"value"`
}

type SimulatedDataEntryUpdatedChange struct {
	SimulatedStateChangeBase
	Name     string `json:"name"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
}

type SimulatedDataEntryRemovedChange struct {
	SimulatedStateChangeBase
	Name     string `json:"name"`
	OldValue string `json:"old_value"`
}

type SimulatedAllowanceChange struct {
	SimulatedStateChangeBase
	TokenID          string `json:"token_id"`
	Spender          string `json:"spender"`
	Amount           string `json:"amount"`
	ExpirationLedger uint32 `json:"expiration_ledger"`
}

type SimulatedTrustlineAddedChange struct {
	SimulatedStateChangeBase
	TokenID         *string `json:"token_id,omitempty"`
	LiquidityPoolID *string `json:"liquidity_pool_id,omitempty"`
	Limit           string  `json:"limit"`
}

type SimulatedTrustlineUpdatedChange struct {
	SimulatedStateChangeBase
	TokenID         *string `json:"token_id,omitempty"`
	LiquidityPoolID *string `json:"liquidity_pool_id,omitempty"`
	OldLimit        string  `json:"old_limit"`
	NewLimit        string  `json:"new_limit"`
}

type SimulatedTrustlineRemovedChange struct {
	SimulatedStateChangeBase
	TokenID         *string `json:"token_id,omitempty"`
	LiquidityPoolID *string `json:"liquidity_pool_id,omitempty"`
}

type SimulatedBalanceAuthorizationChange struct {
	SimulatedStateChangeBase
	TokenID         *string  `json:"token_id,omitempty"`
	LiquidityPoolID *string  `json:"liquidity_pool_id,omitempty"`
	Flags           []string `json:"flags,omitempty"`
}
