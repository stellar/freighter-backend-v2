// ABOUTME: Maps wallet-backend SDK transaction/operation/state-change types into freighter snake_case REST types.
// ABOUTME: mapStateChange is the only non-trivial mapper — a type switch over the 19 SDK state-change variants.
package services

import (
	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// mapTransaction copies an SDK transaction into the snake_case REST shape.
func mapTransaction(t *wbtypes.GraphQLTransaction) types.Transaction {
	return types.Transaction{
		Hash:            t.Hash,
		FeeCharged:      t.FeeCharged,
		ResultCode:      t.ResultCode,
		LedgerNumber:    t.LedgerNumber,
		LedgerCreatedAt: t.LedgerCreatedAt,
		IsFeeBump:       t.IsFeeBump,
		IngestedAt:      t.IngestedAt,
	}
}

// mapOperation copies an SDK operation into the snake_case REST shape.
func mapOperation(o *wbtypes.Operation) types.Operation {
	return types.Operation{
		ID:              o.ID,
		OperationType:   string(o.Type),
		OperationXDR:    o.OperationXdr,
		ResultCode:      o.ResultCode,
		Successful:      o.Successful,
		LedgerNumber:    o.LedgerNumber,
		LedgerCreatedAt: o.LedgerCreatedAt,
		IngestedAt:      o.IngestedAt,
	}
}

// mapStateChange dispatches an SDK state-change node to the matching freighter
// variant. The SDK's UnmarshalStateChangeNode rejects unknown __typename, so
// the default branch is unreachable in practice; it degrades a hypothetical
// future variant to base fields (with an empty variant) rather than panicking
// or dropping the row.
func mapStateChange(n wbtypes.StateChangeNode) types.StateChange {
	base := types.StateChangeBase{
		Type:            string(n.GetCategory()),
		Reason:          string(n.GetReason()),
		LedgerNumber:    n.GetLedgerNumber(),
		LedgerCreatedAt: n.GetLedgerCreatedAt(),
		IngestedAt:      n.GetIngestedAt(),
	}
	// Variant names the concrete upstream shape. It is a convenience
	// discriminator — each (type, reason) pair maps to exactly one variant — so
	// clients switch on one field instead of the (type, reason) pair.
	switch sc := n.(type) {
	case *wbtypes.BalanceChange:
		base.Variant = "BalanceChange"
		return &types.BalanceChange{StateChangeBase: base, TokenID: sc.TokenID, Amount: sc.Amount, ToMuxedID: sc.ToMuxedID}
	case *wbtypes.AccountCreatedChange:
		base.Variant = "AccountCreatedChange"
		return &types.AccountCreatedChange{StateChangeBase: base, CreatorAddress: sc.CreatorAddress}
	case *wbtypes.AccountMergedChange:
		base.Variant = "AccountMergedChange"
		return &types.AccountMergedChange{StateChangeBase: base, DestinationAddress: sc.DestinationAddress}
	case *wbtypes.SignerAddedChange:
		base.Variant = "SignerAddedChange"
		return &types.SignerAddedChange{StateChangeBase: base, SignerAddress: sc.SignerAddress, NewWeight: sc.NewWeight}
	case *wbtypes.SignerUpdatedChange:
		base.Variant = "SignerUpdatedChange"
		return &types.SignerUpdatedChange{StateChangeBase: base, SignerAddress: sc.SignerAddress, OldWeight: sc.OldWeight, NewWeight: sc.NewWeight}
	case *wbtypes.SignerRemovedChange:
		base.Variant = "SignerRemovedChange"
		return &types.SignerRemovedChange{StateChangeBase: base, SignerAddress: sc.SignerAddress, OldWeight: sc.OldWeight}
	case *wbtypes.ThresholdChange:
		base.Variant = "ThresholdChange"
		return &types.ThresholdChange{StateChangeBase: base, Threshold: string(sc.Threshold), OldThreshold: sc.OldThreshold, NewThreshold: sc.NewThreshold}
	case *wbtypes.AccountFlagsChange:
		base.Variant = "AccountFlagsChange"
		return &types.AccountFlagsChange{StateChangeBase: base, Flags: enumStrings(sc.Flags)}
	case *wbtypes.HomeDomainSetChange:
		base.Variant = "HomeDomainSetChange"
		return &types.HomeDomainSetChange{StateChangeBase: base, HomeDomain: sc.HomeDomain}
	case *wbtypes.HomeDomainUpdatedChange:
		base.Variant = "HomeDomainUpdatedChange"
		return &types.HomeDomainUpdatedChange{StateChangeBase: base, OldHomeDomain: sc.OldHomeDomain, NewHomeDomain: sc.NewHomeDomain}
	case *wbtypes.HomeDomainClearedChange:
		base.Variant = "HomeDomainClearedChange"
		return &types.HomeDomainClearedChange{StateChangeBase: base, OldHomeDomain: sc.OldHomeDomain}
	case *wbtypes.DataEntryAddedChange:
		base.Variant = "DataEntryAddedChange"
		return &types.DataEntryAddedChange{StateChangeBase: base, Name: sc.Name, Value: sc.Value}
	case *wbtypes.DataEntryUpdatedChange:
		base.Variant = "DataEntryUpdatedChange"
		return &types.DataEntryUpdatedChange{StateChangeBase: base, Name: sc.Name, OldValue: sc.OldValue, NewValue: sc.NewValue}
	case *wbtypes.DataEntryRemovedChange:
		base.Variant = "DataEntryRemovedChange"
		return &types.DataEntryRemovedChange{StateChangeBase: base, Name: sc.Name, OldValue: sc.OldValue}
	case *wbtypes.AllowanceChange:
		base.Variant = "AllowanceChange"
		return &types.AllowanceChange{StateChangeBase: base, TokenID: sc.TokenID, Spender: sc.Spender, Amount: sc.Amount, ExpirationLedger: sc.ExpirationLedger}
	case *wbtypes.TrustlineAddedChange:
		base.Variant = "TrustlineAddedChange"
		return &types.TrustlineAddedChange{StateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID, Limit: sc.Limit}
	case *wbtypes.TrustlineUpdatedChange:
		base.Variant = "TrustlineUpdatedChange"
		return &types.TrustlineUpdatedChange{StateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID, OldLimit: sc.OldLimit, NewLimit: sc.NewLimit}
	case *wbtypes.TrustlineRemovedChange:
		base.Variant = "TrustlineRemovedChange"
		return &types.TrustlineRemovedChange{StateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID}
	case *wbtypes.BalanceAuthorizationChange:
		base.Variant = "BalanceAuthorizationChange"
		return &types.BalanceAuthorizationChange{StateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID, Flags: enumStrings(sc.Flags)}
	default:
		return &base
	}
}

// enumStrings converts a slice of SDK string enums to their string
// representations. Always returns a non-nil slice; whether an empty result is
// emitted as [] or omitted from the JSON is decided by the target field's tag,
// not by this function. AccountFlagsChange.Flags is `json:"flags"` (upstream
// [AccountFlag!]!) so it encodes []; BalanceAuthorizationChange.Flags is
// `json:"flags,omitempty"` (upstream [TrustlineFlag!], null for SAC
// contract-holder authorization) so the key is dropped — encoding/json omits any
// zero-length slice, nil or not.
func enumStrings[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

// mapAccountTransactionEdge flattens one SDK edge into an AccountTransaction.
// Nil operation/state-change entries are skipped; the result slices are always
// non-nil so the JSON encoder emits [] rather than null.
func mapAccountTransactionEdge(e *wbtypes.AccountTransactionEdge) *types.AccountTransaction {
	ops := make([]types.Operation, 0, len(e.Operations))
	for _, o := range e.Operations {
		if o == nil {
			continue
		}
		ops = append(ops, mapOperation(o))
	}
	scs := make([]types.StateChange, 0, len(e.StateChanges))
	for _, sc := range e.StateChanges {
		if sc == nil {
			continue
		}
		scs = append(scs, mapStateChange(sc))
	}
	return &types.AccountTransaction{
		Transaction:  mapTransaction(e.Node),
		Operations:   ops,
		StateChanges: scs,
	}
}
