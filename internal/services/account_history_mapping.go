// ABOUTME: Maps wallet-backend SDK transaction/operation/state-change types into freighter snake_case REST types.
// ABOUTME: mapStateChange is the only non-trivial mapper — a type switch over the 9 SDK state-change variants.
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
		Type:            string(n.GetType()),
		Reason:          string(n.GetReason()),
		LedgerNumber:    n.GetLedgerNumber(),
		LedgerCreatedAt: n.GetLedgerCreatedAt(),
		IngestedAt:      n.GetIngestedAt(),
	}
	switch sc := n.(type) {
	case *wbtypes.StandardBalanceChange:
		return &types.StandardBalanceChange{StateChangeBase: base, StandardBalanceTokenID: sc.TokenID, Amount: sc.Amount}
	case *wbtypes.AccountChange:
		return &types.AccountChange{StateChangeBase: base, FunderAddress: sc.FunderAddress}
	case *wbtypes.SignerChange:
		return &types.SignerChange{StateChangeBase: base, SignerAddress: sc.SignerAddress, SignerWeights: sc.SignerWeights}
	case *wbtypes.SignerThresholdsChange:
		return &types.SignerThresholdsChange{StateChangeBase: base, Thresholds: sc.Thresholds}
	case *wbtypes.MetadataChange:
		return &types.MetadataChange{StateChangeBase: base, MetadataKeyValue: sc.KeyValue}
	case *wbtypes.FlagsChange:
		return &types.FlagsChange{StateChangeBase: base, Flags: sc.Flags}
	case *wbtypes.TrustlineChange:
		return &types.TrustlineChange{StateChangeBase: base, TrustlineTokenID: sc.TokenID, Limit: sc.Limit, TrustlineLiquidityPoolID: sc.LiquidityPoolID}
	case *wbtypes.ReservesChange:
		return &types.ReservesChange{StateChangeBase: base, SponsoredAddress: sc.SponsoredAddress, SponsorAddress: sc.SponsorAddress, SponsoredData: sc.SponsoredData, SponsoredTrustline: sc.SponsoredTrustline, ClaimableBalanceID: sc.ClaimableBalanceID, LiquidityPoolID: sc.LiquidityPoolID}
	case *wbtypes.BalanceAuthorizationChange:
		return &types.BalanceAuthorizationChange{StateChangeBase: base, BalanceAuthTokenID: sc.TokenID, BalanceAuthLiquidityPoolID: sc.LiquidityPoolID, Flags: sc.Flags}
	default:
		return &base
	}
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
