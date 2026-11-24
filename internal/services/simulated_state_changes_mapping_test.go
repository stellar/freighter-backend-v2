// ABOUTME: Unit tests for the wbclient -> freighter mapping of simulated state changes.
// ABOUTME: Covers all 19 simulated variants and the result envelope.
package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

func TestMapSimulatedStateChange_AllVariants(t *testing.T) {
	t.Parallel()
	opIndex := int32(1)
	base := wbtypes.BaseSimulatedStateChangeFields{
		Category: wbtypes.StateChangeCategoryBalance, Reason: wbtypes.StateChangeReasonDebit,
		AccountAddress: "GACCOUNT", OperationIndex: &opIndex,
	}
	variantBase := func(v string) types.SimulatedStateChangeBase {
		return types.SimulatedStateChangeBase{Variant: v, Type: "BALANCE", Reason: "DEBIT", AccountAddress: "GACCOUNT", OperationIndex: &opIndex}
	}
	token, pool, muxed := "CTOKEN", "POOL", "42"

	cases := []struct {
		name string
		in   wbtypes.SimulatedStateChangeNode
		want types.SimulatedStateChange
	}{
		{
			"BalanceChange",
			&wbtypes.SimulatedBalanceChange{BaseSimulatedStateChangeFields: base, TokenID: token, Amount: "10", ToMuxedID: &muxed},
			&types.SimulatedBalanceChange{SimulatedStateChangeBase: variantBase("BalanceChange"), TokenID: token, Amount: "10", ToMuxedID: &muxed},
		},
		{
			"AccountCreatedChange",
			&wbtypes.SimulatedAccountCreatedChange{BaseSimulatedStateChangeFields: base, CreatorAddress: "GFUNDER"},
			&types.SimulatedAccountCreatedChange{SimulatedStateChangeBase: variantBase("AccountCreatedChange"), CreatorAddress: "GFUNDER"},
		},
		{
			"AccountMergedChange",
			&wbtypes.SimulatedAccountMergedChange{BaseSimulatedStateChangeFields: base, DestinationAddress: "GDEST"},
			&types.SimulatedAccountMergedChange{SimulatedStateChangeBase: variantBase("AccountMergedChange"), DestinationAddress: "GDEST"},
		},
		{
			"SignerAddedChange",
			&wbtypes.SimulatedSignerAddedChange{BaseSimulatedStateChangeFields: base, SignerAddress: "GSIGNER", NewWeight: 2},
			&types.SimulatedSignerAddedChange{SimulatedStateChangeBase: variantBase("SignerAddedChange"), SignerAddress: "GSIGNER", NewWeight: 2},
		},
		{
			"SignerUpdatedChange",
			&wbtypes.SimulatedSignerUpdatedChange{BaseSimulatedStateChangeFields: base, SignerAddress: "GSIGNER", OldWeight: 1, NewWeight: 2},
			&types.SimulatedSignerUpdatedChange{SimulatedStateChangeBase: variantBase("SignerUpdatedChange"), SignerAddress: "GSIGNER", OldWeight: 1, NewWeight: 2},
		},
		{
			"SignerRemovedChange",
			&wbtypes.SimulatedSignerRemovedChange{BaseSimulatedStateChangeFields: base, SignerAddress: "GSIGNER", OldWeight: 1},
			&types.SimulatedSignerRemovedChange{SimulatedStateChangeBase: variantBase("SignerRemovedChange"), SignerAddress: "GSIGNER", OldWeight: 1},
		},
		{
			"ThresholdChange",
			&wbtypes.SimulatedThresholdChange{BaseSimulatedStateChangeFields: base, Threshold: wbtypes.ThresholdLevel("MEDIUM"), OldThreshold: 1, NewThreshold: 2},
			&types.SimulatedThresholdChange{SimulatedStateChangeBase: variantBase("ThresholdChange"), Threshold: "MEDIUM", OldThreshold: 1, NewThreshold: 2},
		},
		{
			"AccountFlagsChange",
			&wbtypes.SimulatedAccountFlagsChange{BaseSimulatedStateChangeFields: base, Flags: []wbtypes.AccountFlag{wbtypes.AccountFlagAuthRequired}},
			&types.SimulatedAccountFlagsChange{SimulatedStateChangeBase: variantBase("AccountFlagsChange"), Flags: []string{"AUTH_REQUIRED"}},
		},
		{
			"HomeDomainSetChange",
			&wbtypes.SimulatedHomeDomainSetChange{BaseSimulatedStateChangeFields: base, HomeDomain: "a.com"},
			&types.SimulatedHomeDomainSetChange{SimulatedStateChangeBase: variantBase("HomeDomainSetChange"), HomeDomain: "a.com"},
		},
		{
			"HomeDomainUpdatedChange",
			&wbtypes.SimulatedHomeDomainUpdatedChange{BaseSimulatedStateChangeFields: base, OldHomeDomain: "a.com", NewHomeDomain: "b.com"},
			&types.SimulatedHomeDomainUpdatedChange{SimulatedStateChangeBase: variantBase("HomeDomainUpdatedChange"), OldHomeDomain: "a.com", NewHomeDomain: "b.com"},
		},
		{
			"HomeDomainClearedChange",
			&wbtypes.SimulatedHomeDomainClearedChange{BaseSimulatedStateChangeFields: base, OldHomeDomain: "a.com"},
			&types.SimulatedHomeDomainClearedChange{SimulatedStateChangeBase: variantBase("HomeDomainClearedChange"), OldHomeDomain: "a.com"},
		},
		{
			"DataEntryAddedChange",
			&wbtypes.SimulatedDataEntryAddedChange{BaseSimulatedStateChangeFields: base, Name: "k", Value: "dg=="},
			&types.SimulatedDataEntryAddedChange{SimulatedStateChangeBase: variantBase("DataEntryAddedChange"), Name: "k", Value: "dg=="},
		},
		{
			"DataEntryUpdatedChange",
			&wbtypes.SimulatedDataEntryUpdatedChange{BaseSimulatedStateChangeFields: base, Name: "k", OldValue: "YQ==", NewValue: "Yg=="},
			&types.SimulatedDataEntryUpdatedChange{SimulatedStateChangeBase: variantBase("DataEntryUpdatedChange"), Name: "k", OldValue: "YQ==", NewValue: "Yg=="},
		},
		{
			"DataEntryRemovedChange",
			&wbtypes.SimulatedDataEntryRemovedChange{BaseSimulatedStateChangeFields: base, Name: "k", OldValue: "YQ=="},
			&types.SimulatedDataEntryRemovedChange{SimulatedStateChangeBase: variantBase("DataEntryRemovedChange"), Name: "k", OldValue: "YQ=="},
		},
		{
			"AllowanceChange",
			&wbtypes.SimulatedAllowanceChange{BaseSimulatedStateChangeFields: base, TokenID: token, Spender: "GSPENDER", Amount: "5", ExpirationLedger: 99},
			&types.SimulatedAllowanceChange{SimulatedStateChangeBase: variantBase("AllowanceChange"), TokenID: token, Spender: "GSPENDER", Amount: "5", ExpirationLedger: 99},
		},
		{
			"TrustlineAddedChange",
			&wbtypes.SimulatedTrustlineAddedChange{BaseSimulatedStateChangeFields: base, TokenID: &token, Limit: "100"},
			&types.SimulatedTrustlineAddedChange{SimulatedStateChangeBase: variantBase("TrustlineAddedChange"), TokenID: &token, Limit: "100"},
		},
		{
			"TrustlineUpdatedChange",
			&wbtypes.SimulatedTrustlineUpdatedChange{BaseSimulatedStateChangeFields: base, LiquidityPoolID: &pool, OldLimit: "100", NewLimit: "200"},
			&types.SimulatedTrustlineUpdatedChange{SimulatedStateChangeBase: variantBase("TrustlineUpdatedChange"), LiquidityPoolID: &pool, OldLimit: "100", NewLimit: "200"},
		},
		{
			"TrustlineRemovedChange",
			&wbtypes.SimulatedTrustlineRemovedChange{BaseSimulatedStateChangeFields: base, TokenID: &token},
			&types.SimulatedTrustlineRemovedChange{SimulatedStateChangeBase: variantBase("TrustlineRemovedChange"), TokenID: &token},
		},
		{
			"BalanceAuthorizationChange",
			&wbtypes.SimulatedBalanceAuthorizationChange{BaseSimulatedStateChangeFields: base, TokenID: &token, Flags: []wbtypes.TrustlineFlag{"AUTHORIZED"}},
			&types.SimulatedBalanceAuthorizationChange{SimulatedStateChangeBase: variantBase("BalanceAuthorizationChange"), TokenID: &token, Flags: []string{"AUTHORIZED"}},
		},
	}
	require.Len(t, cases, 19, "one case per simulated variant")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, mapSimulatedStateChange(tc.in))
		})
	}
}

func TestMapSimulatedStateChanges_Envelope(t *testing.T) {
	t.Parallel()
	code := "USDC"

	t.Run("copies ledger, state changes and tokens", func(t *testing.T) {
		t.Parallel()
		opIndex := int32(0)
		got := mapSimulatedStateChanges(&wbtypes.SimulatedStateChanges{
			LatestLedger: 7,
			StateChanges: []wbtypes.SimulatedStateChangeNode{
				&wbtypes.SimulatedBalanceChange{BaseSimulatedStateChangeFields: wbtypes.BaseSimulatedStateChangeFields{AccountAddress: "GFEE"}, TokenID: "CXLM", Amount: "100"},
				nil,
				&wbtypes.SimulatedBalanceChange{BaseSimulatedStateChangeFields: wbtypes.BaseSimulatedStateChangeFields{AccountAddress: "GSENDER", OperationIndex: &opIndex}, TokenID: "CUSDC", Amount: "5"},
			},
			Tokens: []wbtypes.SimulatedTokenMetadata{{TokenID: "CUSDC", Code: &code, Decimals: 7}},
		})
		assert.Equal(t, uint32(7), got.LatestLedger)
		require.Len(t, got.StateChanges, 1, "the fee row and nil entries are skipped")
		debit, ok := got.StateChanges[0].(*types.SimulatedBalanceChange)
		require.True(t, ok)
		assert.Equal(t, "GSENDER", debit.AccountAddress)
		require.NotNil(t, debit.OperationIndex)
		assert.Equal(t, []types.SimulatedToken{{TokenID: "CUSDC", Code: &code, Decimals: 7}}, got.Tokens)
	})

	t.Run("a result with only the fee row maps to no state changes", func(t *testing.T) {
		t.Parallel()
		got := mapSimulatedStateChanges(&wbtypes.SimulatedStateChanges{
			StateChanges: []wbtypes.SimulatedStateChangeNode{
				&wbtypes.SimulatedBalanceChange{BaseSimulatedStateChangeFields: wbtypes.BaseSimulatedStateChangeFields{AccountAddress: "GFEE"}, TokenID: "CXLM", Amount: "100"},
			},
		})
		assert.NotNil(t, got.StateChanges)
		assert.Empty(t, got.StateChanges)
	})

	t.Run("empty result encodes as empty slices, not nil", func(t *testing.T) {
		t.Parallel()
		got := mapSimulatedStateChanges(&wbtypes.SimulatedStateChanges{})
		assert.NotNil(t, got.StateChanges)
		assert.Empty(t, got.StateChanges)
		assert.NotNil(t, got.Tokens)
		assert.Empty(t, got.Tokens)
	})
}
