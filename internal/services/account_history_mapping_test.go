// ABOUTME: Unit tests for the wbclient -> freighter snake_case mapping helpers.
// ABOUTME: Covers transaction/operation field mapping, all 19 state-change variants, and edge flattening.
package services

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

func TestMapTransactionAndOperation(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	tx := mapTransaction(&wbtypes.GraphQLTransaction{
		Hash: "h1", FeeCharged: 100, ResultCode: "txSUCCESS",
		LedgerNumber: 42, LedgerCreatedAt: ts, IsFeeBump: true, IngestedAt: ts,
	})
	assert.Equal(t, types.Transaction{Hash: "h1", FeeCharged: 100, ResultCode: "txSUCCESS", LedgerNumber: 42, LedgerCreatedAt: ts, IsFeeBump: true, IngestedAt: ts}, tx)

	op := mapOperation(&wbtypes.Operation{
		ID: 7, Type: wbtypes.OperationTypePayment, OperationXdr: "AAA",
		ResultCode: "opSUCCESS", Successful: true, LedgerNumber: 42, LedgerCreatedAt: ts, IngestedAt: ts,
	})
	assert.Equal(t, types.Operation{ID: 7, OperationType: "PAYMENT", OperationXDR: "AAA", ResultCode: "opSUCCESS", Successful: true, LedgerNumber: 42, LedgerCreatedAt: ts, IngestedAt: ts}, op)
}

func TestMapStateChange_AllVariants(t *testing.T) {
	t.Parallel()
	base := wbtypes.BaseStateChangeFields{Category: wbtypes.StateChangeCategoryBalance, Reason: wbtypes.StateChangeReasonDebit}
	wantBase := types.StateChangeBase{Type: "BALANCE", Reason: "DEBIT"}
	variantBase := func(v string) types.StateChangeBase {
		b := wantBase
		b.Variant = v
		return b
	}
	s := "x"
	oldW, newW := int32(1), int32(2)
	cases := []struct {
		name string
		in   wbtypes.StateChangeNode
		want types.StateChange
	}{
		{
			"balance", &wbtypes.BalanceChange{BaseStateChangeFields: base, TokenID: "native", Amount: "10", ToMuxedID: &s},
			&types.BalanceChange{StateChangeBase: variantBase("BalanceChange"), TokenID: "native", Amount: "10", ToMuxedID: &s},
		},
		{
			// A transaction fee arrives as a BalanceChange with no muxed destination.
			"balance_fee", &wbtypes.BalanceChange{BaseStateChangeFields: base, TokenID: "native", Amount: "1"},
			&types.BalanceChange{StateChangeBase: variantBase("BalanceChange"), TokenID: "native", Amount: "1"},
		},
		{
			// One variant serves both classic account creation (creator = funding
			// account) and contract deployment (creator = deploying address).
			"account_created", &wbtypes.AccountCreatedChange{BaseStateChangeFields: base, CreatorAddress: "GFUNDER"},
			&types.AccountCreatedChange{StateChangeBase: variantBase("AccountCreatedChange"), CreatorAddress: "GFUNDER"},
		},
		{
			"account_merged", &wbtypes.AccountMergedChange{BaseStateChangeFields: base, DestinationAddress: "GDEST"},
			&types.AccountMergedChange{StateChangeBase: variantBase("AccountMergedChange"), DestinationAddress: "GDEST"},
		},
		{
			"signer_added", &wbtypes.SignerAddedChange{BaseStateChangeFields: base, SignerAddress: "GSIGN", NewWeight: newW},
			&types.SignerAddedChange{StateChangeBase: variantBase("SignerAddedChange"), SignerAddress: "GSIGN", NewWeight: newW},
		},
		{
			"signer_updated", &wbtypes.SignerUpdatedChange{BaseStateChangeFields: base, SignerAddress: "GSIGN", OldWeight: oldW, NewWeight: newW},
			&types.SignerUpdatedChange{StateChangeBase: variantBase("SignerUpdatedChange"), SignerAddress: "GSIGN", OldWeight: oldW, NewWeight: newW},
		},
		{
			"signer_removed", &wbtypes.SignerRemovedChange{BaseStateChangeFields: base, SignerAddress: "GSIGN", OldWeight: oldW},
			&types.SignerRemovedChange{StateChangeBase: variantBase("SignerRemovedChange"), SignerAddress: "GSIGN", OldWeight: oldW},
		},
		{
			"threshold", &wbtypes.ThresholdChange{BaseStateChangeFields: base, Threshold: wbtypes.ThresholdLevelMedium, OldThreshold: oldW, NewThreshold: newW},
			&types.ThresholdChange{StateChangeBase: variantBase("ThresholdChange"), Threshold: "MEDIUM", OldThreshold: oldW, NewThreshold: newW},
		},
		{
			"account_flags", &wbtypes.AccountFlagsChange{BaseStateChangeFields: base, Flags: []wbtypes.AccountFlag{wbtypes.AccountFlagAuthRequired}},
			&types.AccountFlagsChange{StateChangeBase: variantBase("AccountFlagsChange"), Flags: []string{"AUTH_REQUIRED"}},
		},
		{
			"home_domain_set", &wbtypes.HomeDomainSetChange{BaseStateChangeFields: base, HomeDomain: "example.com"},
			&types.HomeDomainSetChange{StateChangeBase: variantBase("HomeDomainSetChange"), HomeDomain: "example.com"},
		},
		{
			"home_domain_updated", &wbtypes.HomeDomainUpdatedChange{BaseStateChangeFields: base, OldHomeDomain: "old.example.com", NewHomeDomain: "new.example.com"},
			&types.HomeDomainUpdatedChange{StateChangeBase: variantBase("HomeDomainUpdatedChange"), OldHomeDomain: "old.example.com", NewHomeDomain: "new.example.com"},
		},
		{
			"home_domain_cleared", &wbtypes.HomeDomainClearedChange{BaseStateChangeFields: base, OldHomeDomain: "example.com"},
			&types.HomeDomainClearedChange{StateChangeBase: variantBase("HomeDomainClearedChange"), OldHomeDomain: "example.com"},
		},
		{
			"data_entry_added", &wbtypes.DataEntryAddedChange{BaseStateChangeFields: base, Name: "k", Value: "v"},
			&types.DataEntryAddedChange{StateChangeBase: variantBase("DataEntryAddedChange"), Name: "k", Value: "v"},
		},
		{
			"data_entry_updated", &wbtypes.DataEntryUpdatedChange{BaseStateChangeFields: base, Name: "k", OldValue: "v1", NewValue: "v2"},
			&types.DataEntryUpdatedChange{StateChangeBase: variantBase("DataEntryUpdatedChange"), Name: "k", OldValue: "v1", NewValue: "v2"},
		},
		{
			"data_entry_removed", &wbtypes.DataEntryRemovedChange{BaseStateChangeFields: base, Name: "k", OldValue: "v1"},
			&types.DataEntryRemovedChange{StateChangeBase: variantBase("DataEntryRemovedChange"), Name: "k", OldValue: "v1"},
		},
		{
			"allowance", &wbtypes.AllowanceChange{BaseStateChangeFields: base, TokenID: "CTOKEN", Spender: "GSPEND", Amount: "5", ExpirationLedger: 42},
			&types.AllowanceChange{StateChangeBase: variantBase("AllowanceChange"), TokenID: "CTOKEN", Spender: "GSPEND", Amount: "5", ExpirationLedger: 42},
		},
		{
			"trustline_added", &wbtypes.TrustlineAddedChange{BaseStateChangeFields: base, TokenID: &s, LiquidityPoolID: &s, Limit: "100"},
			&types.TrustlineAddedChange{StateChangeBase: variantBase("TrustlineAddedChange"), TokenID: &s, LiquidityPoolID: &s, Limit: "100"},
		},
		{
			"trustline_updated", &wbtypes.TrustlineUpdatedChange{BaseStateChangeFields: base, TokenID: &s, LiquidityPoolID: &s, OldLimit: "10", NewLimit: "20"},
			&types.TrustlineUpdatedChange{StateChangeBase: variantBase("TrustlineUpdatedChange"), TokenID: &s, LiquidityPoolID: &s, OldLimit: "10", NewLimit: "20"},
		},
		{
			"trustline_removed", &wbtypes.TrustlineRemovedChange{BaseStateChangeFields: base, TokenID: &s, LiquidityPoolID: &s},
			&types.TrustlineRemovedChange{StateChangeBase: variantBase("TrustlineRemovedChange"), TokenID: &s, LiquidityPoolID: &s},
		},
		{
			"balance_authorization", &wbtypes.BalanceAuthorizationChange{BaseStateChangeFields: base, TokenID: &s, LiquidityPoolID: &s, Flags: []wbtypes.TrustlineFlag{wbtypes.TrustlineFlagAuthorized}},
			&types.BalanceAuthorizationChange{StateChangeBase: variantBase("BalanceAuthorizationChange"), TokenID: &s, LiquidityPoolID: &s, Flags: []string{"AUTHORIZED"}},
		},
		{
			"balance_authorization_nil_flags", &wbtypes.BalanceAuthorizationChange{BaseStateChangeFields: base, TokenID: &s},
			&types.BalanceAuthorizationChange{StateChangeBase: variantBase("BalanceAuthorizationChange"), TokenID: &s},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, mapStateChange(tc.in))
		})
	}
}

func TestMapAccountTransactionEdge_FlattensAndGuaranteesNonNilSlices(t *testing.T) {
	t.Parallel()
	edge := &wbtypes.AccountTransactionEdge{
		Node:         &wbtypes.GraphQLTransaction{Hash: "h1"},
		Cursor:       "c1",
		Operations:   []*wbtypes.Operation{{ID: 1, Type: wbtypes.OperationTypePayment}, nil},
		StateChanges: []wbtypes.StateChangeNode{&wbtypes.BalanceChange{BaseStateChangeFields: wbtypes.BaseStateChangeFields{Category: wbtypes.StateChangeCategoryBalance}, Amount: "5"}, nil},
	}
	got := mapAccountTransactionEdge(edge)
	assert.Equal(t, "h1", got.Hash)
	require.Len(t, got.Operations, 1, "nil operation entry skipped")
	assert.Equal(t, int64(1), got.Operations[0].ID)
	require.Len(t, got.StateChanges, 1, "nil state-change entry skipped")

	empty := mapAccountTransactionEdge(&wbtypes.AccountTransactionEdge{Node: &wbtypes.GraphQLTransaction{Hash: "h2"}})
	assert.NotNil(t, empty.Operations)
	assert.NotNil(t, empty.StateChanges)
	assert.Empty(t, empty.Operations)
	assert.Empty(t, empty.StateChanges)
}
