// ABOUTME: Maps wallet-backend SDK transaction/operation/state-change types into freighter snake_case REST types.
// ABOUTME: mapStateChange is the only non-trivial mapper — a type switch over the 18 SDK state-change variants.
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
		OperationType:   string(o.OperationType),
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
// future variant to type+reason rather than panicking or dropping the row.
func mapStateChange(n wbtypes.StateChangeNode) types.StateChange {
	base := types.StateChangeBase{
		Type:            string(n.GetCategory()),
		Reason:          string(n.GetReason()),
		LedgerNumber:    n.GetLedgerNumber(),
		LedgerCreatedAt: n.GetLedgerCreatedAt(),
		IngestedAt:      n.GetIngestedAt(),
	}
	switch sc := n.(type) {
	case *wbtypes.BalanceChange:
		return &types.BalanceChange{StateChangeBase: base, TokenID: sc.TokenID, Amount: sc.Amount, ToMuxedID: sc.ToMuxedID}
	case *wbtypes.FeeChange:
		return &types.FeeChange{StateChangeBase: base, TokenID: sc.TokenID, Amount: sc.Amount}
	case *wbtypes.AccountCreatedChange:
		return &types.AccountCreatedChange{StateChangeBase: base, FunderAddress: sc.FunderAddress}
	case *wbtypes.ContractDeployedChange:
		return &types.ContractDeployedChange{StateChangeBase: base, DeployerAddress: sc.DeployerAddress}
	case *wbtypes.AccountMergedChange:
		return &types.AccountMergedChange{StateChangeBase: base, DestinationAddress: sc.DestinationAddress}
	case *wbtypes.SignerAddedChange:
		return &types.SignerAddedChange{StateChangeBase: base, SignerAddress: sc.SignerAddress, NewWeight: sc.NewWeight}
	case *wbtypes.SignerUpdatedChange:
		return &types.SignerUpdatedChange{StateChangeBase: base, SignerAddress: sc.SignerAddress, OldWeight: sc.OldWeight, NewWeight: sc.NewWeight}
	case *wbtypes.SignerRemovedChange:
		return &types.SignerRemovedChange{StateChangeBase: base, SignerAddress: sc.SignerAddress, OldWeight: sc.OldWeight}
	case *wbtypes.ThresholdChange:
		return &types.ThresholdChange{StateChangeBase: base, OldThreshold: sc.OldThreshold, NewThreshold: sc.NewThreshold}
	case *wbtypes.AccountFlagsChange:
		return &types.AccountFlagsChange{StateChangeBase: base, Flags: mapAccountFlags(sc.Flags)}
	case *wbtypes.HomeDomainChange:
		return &types.HomeDomainChange{StateChangeBase: base, OldHomeDomain: sc.OldHomeDomain, NewHomeDomain: sc.NewHomeDomain}
	case *wbtypes.DataEntryChange:
		return &types.DataEntryChange{StateChangeBase: base, Name: sc.Name, OldValue: sc.OldValue, NewValue: sc.NewValue}
	case *wbtypes.AllowanceChange:
		return &types.AllowanceChange{StateChangeBase: base, TokenID: sc.TokenID, Spender: sc.Spender, Amount: sc.Amount, ExpirationLedger: sc.ExpirationLedger}
	case *wbtypes.TrustlineAddedChange:
		return &types.TrustlineAddedChange{StateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID, Limit: sc.Limit}
	case *wbtypes.TrustlineUpdatedChange:
		return &types.TrustlineUpdatedChange{StateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID, OldLimit: sc.OldLimit, NewLimit: sc.NewLimit}
	case *wbtypes.TrustlineRemovedChange:
		return &types.TrustlineRemovedChange{StateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID}
	case *wbtypes.SponsorshipChange:
		return &types.SponsorshipChange{StateChangeBase: base, SponsoredAddress: sc.SponsoredAddress, SponsorAddress: sc.SponsorAddress, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID, ClaimableBalanceID: sc.ClaimableBalanceID, DataName: sc.DataName, SignerAddress: sc.SignerAddress}
	case *wbtypes.BalanceAuthorizationChange:
		return &types.BalanceAuthorizationChange{StateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID, Flags: mapTrustlineFlags(sc.Flags)}
	default:
		return &base
	}
}

// mapAccountFlags converts SDK account flags to their string representations.
// Returns nil for an empty input so the REST field marshals consistently.
func mapAccountFlags(flags []wbtypes.AccountFlag) []string {
	if len(flags) == 0 {
		return nil
	}
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = string(f)
	}
	return out
}

// mapTrustlineFlags converts SDK trustline flags to their string
// representations. Returns nil for a nil/empty input (SAC contract-holder
// authorization carries no flags), which the REST field omits.
func mapTrustlineFlags(flags []wbtypes.TrustlineFlag) []string {
	if len(flags) == 0 {
		return nil
	}
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = string(f)
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
