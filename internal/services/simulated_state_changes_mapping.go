// ABOUTME: Maps wallet-backend SDK simulated state changes into freighter snake_case REST types.
// ABOUTME: mapSimulatedStateChange is a type switch over the 19 SDK simulated variants.
package services

import (
	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// mapSimulatedStateChanges copies an SDK simulation result into the REST
// shape. The result slices are always non-nil so they encode as [].
func mapSimulatedStateChanges(r *wbtypes.SimulatedStateChanges) *types.SimulatedStateChanges {
	stateChanges := make([]types.SimulatedStateChange, 0, len(r.StateChanges))
	for _, sc := range r.StateChanges {
		if sc == nil {
			continue
		}
		stateChanges = append(stateChanges, mapSimulatedStateChange(sc))
	}
	tokens := make([]types.SimulatedToken, 0, len(r.Tokens))
	for _, t := range r.Tokens {
		tokens = append(tokens, types.SimulatedToken{
			TokenID: t.TokenID, Code: t.Code, Issuer: t.Issuer, Name: t.Name, Symbol: t.Symbol, Decimals: t.Decimals,
		})
	}
	return &types.SimulatedStateChanges{
		LatestLedger: r.LatestLedger,
		StateChanges: stateChanges,
		Tokens:       tokens,
	}
}

// mapSimulatedStateChange dispatches an SDK simulated state change to the
// matching freighter variant. Variant names match mapStateChange. The SDK
// rejects unknown types, so the default branch only guards a future variant.
func mapSimulatedStateChange(n wbtypes.SimulatedStateChangeNode) types.SimulatedStateChange {
	base := types.SimulatedStateChangeBase{
		Type:           string(n.GetCategory()),
		Reason:         string(n.GetReason()),
		AccountAddress: n.GetAccountAddress(),
		OperationIndex: n.GetOperationIndex(),
	}
	switch sc := n.(type) {
	case *wbtypes.SimulatedBalanceChange:
		base.Variant = "BalanceChange"
		return &types.SimulatedBalanceChange{SimulatedStateChangeBase: base, TokenID: sc.TokenID, Amount: sc.Amount, ToMuxedID: sc.ToMuxedID}
	case *wbtypes.SimulatedAccountCreatedChange:
		base.Variant = "AccountCreatedChange"
		return &types.SimulatedAccountCreatedChange{SimulatedStateChangeBase: base, CreatorAddress: sc.CreatorAddress}
	case *wbtypes.SimulatedAccountMergedChange:
		base.Variant = "AccountMergedChange"
		return &types.SimulatedAccountMergedChange{SimulatedStateChangeBase: base, DestinationAddress: sc.DestinationAddress}
	case *wbtypes.SimulatedSignerAddedChange:
		base.Variant = "SignerAddedChange"
		return &types.SimulatedSignerAddedChange{SimulatedStateChangeBase: base, SignerAddress: sc.SignerAddress, NewWeight: sc.NewWeight}
	case *wbtypes.SimulatedSignerUpdatedChange:
		base.Variant = "SignerUpdatedChange"
		return &types.SimulatedSignerUpdatedChange{SimulatedStateChangeBase: base, SignerAddress: sc.SignerAddress, OldWeight: sc.OldWeight, NewWeight: sc.NewWeight}
	case *wbtypes.SimulatedSignerRemovedChange:
		base.Variant = "SignerRemovedChange"
		return &types.SimulatedSignerRemovedChange{SimulatedStateChangeBase: base, SignerAddress: sc.SignerAddress, OldWeight: sc.OldWeight}
	case *wbtypes.SimulatedThresholdChange:
		base.Variant = "ThresholdChange"
		return &types.SimulatedThresholdChange{SimulatedStateChangeBase: base, Threshold: string(sc.Threshold), OldThreshold: sc.OldThreshold, NewThreshold: sc.NewThreshold}
	case *wbtypes.SimulatedAccountFlagsChange:
		base.Variant = "AccountFlagsChange"
		return &types.SimulatedAccountFlagsChange{SimulatedStateChangeBase: base, Flags: enumStrings(sc.Flags)}
	case *wbtypes.SimulatedHomeDomainSetChange:
		base.Variant = "HomeDomainSetChange"
		return &types.SimulatedHomeDomainSetChange{SimulatedStateChangeBase: base, HomeDomain: sc.HomeDomain}
	case *wbtypes.SimulatedHomeDomainUpdatedChange:
		base.Variant = "HomeDomainUpdatedChange"
		return &types.SimulatedHomeDomainUpdatedChange{SimulatedStateChangeBase: base, OldHomeDomain: sc.OldHomeDomain, NewHomeDomain: sc.NewHomeDomain}
	case *wbtypes.SimulatedHomeDomainClearedChange:
		base.Variant = "HomeDomainClearedChange"
		return &types.SimulatedHomeDomainClearedChange{SimulatedStateChangeBase: base, OldHomeDomain: sc.OldHomeDomain}
	case *wbtypes.SimulatedDataEntryAddedChange:
		base.Variant = "DataEntryAddedChange"
		return &types.SimulatedDataEntryAddedChange{SimulatedStateChangeBase: base, Name: sc.Name, Value: sc.Value}
	case *wbtypes.SimulatedDataEntryUpdatedChange:
		base.Variant = "DataEntryUpdatedChange"
		return &types.SimulatedDataEntryUpdatedChange{SimulatedStateChangeBase: base, Name: sc.Name, OldValue: sc.OldValue, NewValue: sc.NewValue}
	case *wbtypes.SimulatedDataEntryRemovedChange:
		base.Variant = "DataEntryRemovedChange"
		return &types.SimulatedDataEntryRemovedChange{SimulatedStateChangeBase: base, Name: sc.Name, OldValue: sc.OldValue}
	case *wbtypes.SimulatedAllowanceChange:
		base.Variant = "AllowanceChange"
		return &types.SimulatedAllowanceChange{SimulatedStateChangeBase: base, TokenID: sc.TokenID, Spender: sc.Spender, Amount: sc.Amount, ExpirationLedger: sc.ExpirationLedger}
	case *wbtypes.SimulatedTrustlineAddedChange:
		base.Variant = "TrustlineAddedChange"
		return &types.SimulatedTrustlineAddedChange{SimulatedStateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID, Limit: sc.Limit}
	case *wbtypes.SimulatedTrustlineUpdatedChange:
		base.Variant = "TrustlineUpdatedChange"
		return &types.SimulatedTrustlineUpdatedChange{SimulatedStateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID, OldLimit: sc.OldLimit, NewLimit: sc.NewLimit}
	case *wbtypes.SimulatedTrustlineRemovedChange:
		base.Variant = "TrustlineRemovedChange"
		return &types.SimulatedTrustlineRemovedChange{SimulatedStateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID}
	case *wbtypes.SimulatedBalanceAuthorizationChange:
		base.Variant = "BalanceAuthorizationChange"
		return &types.SimulatedBalanceAuthorizationChange{SimulatedStateChangeBase: base, TokenID: sc.TokenID, LiquidityPoolID: sc.LiquidityPoolID, Flags: enumStrings(sc.Flags)}
	default:
		return &base
	}
}
