// ABOUTME: Unit tests for the wbclient -> freighter snake_case mapping helpers.
// ABOUTME: Covers transaction/operation field mapping, all 18 state-change variants, and edge flattening.
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
		ID: 7, OperationType: wbtypes.OperationTypePayment, OperationXdr: "AAA",
		ResultCode: "opSUCCESS", Successful: true, LedgerNumber: 42, LedgerCreatedAt: ts, IngestedAt: ts,
	})
	assert.Equal(t, types.Operation{ID: 7, OperationType: "PAYMENT", OperationXDR: "AAA", ResultCode: "opSUCCESS", Successful: true, LedgerNumber: 42, LedgerCreatedAt: ts, IngestedAt: ts}, op)
}

func TestMapStateChange_AllVariants(t *testing.T) {
	t.Parallel()
	base := wbtypes.BaseStateChangeFields{Category: wbtypes.StateChangeCategoryBalance, Reason: wbtypes.StateChangeReasonDebit}
	wantBase := types.StateChangeBase{Type: "BALANCE", Reason: "DEBIT"}
	s := "x"
	oldW, newW := int32(1), int32(2)
	cases := []struct {
		name string
		in   wbtypes.StateChangeNode
		want types.StateChange
	}{
		{
			"balance", &wbtypes.BalanceChange{BaseStateChangeFields: base, TokenID: "native", Amount: "10", ToMuxedID: &s},
			&types.BalanceChange{StateChangeBase: wantBase, TokenID: "native", Amount: "10", ToMuxedID: &s},
		},
		{
			"fee", &wbtypes.FeeChange{BaseStateChangeFields: base, TokenID: "native", Amount: "1"},
			&types.FeeChange{StateChangeBase: wantBase, TokenID: "native", Amount: "1"},
		},
		{
			"account_created", &wbtypes.AccountCreatedChange{BaseStateChangeFields: base, FunderAddress: "GFUNDER"},
			&types.AccountCreatedChange{StateChangeBase: wantBase, FunderAddress: "GFUNDER"},
		},
		{
			"contract_deployed", &wbtypes.ContractDeployedChange{BaseStateChangeFields: base, DeployerAddress: "GDEPLOY"},
			&types.ContractDeployedChange{StateChangeBase: wantBase, DeployerAddress: "GDEPLOY"},
		},
		{
			"account_merged", &wbtypes.AccountMergedChange{BaseStateChangeFields: base, DestinationAddress: "GDEST"},
			&types.AccountMergedChange{StateChangeBase: wantBase, DestinationAddress: "GDEST"},
		},
		{
			"signer_added", &wbtypes.SignerAddedChange{BaseStateChangeFields: base, SignerAddress: "GSIGN", NewWeight: newW},
			&types.SignerAddedChange{StateChangeBase: wantBase, SignerAddress: "GSIGN", NewWeight: newW},
		},
		{
			"signer_updated", &wbtypes.SignerUpdatedChange{BaseStateChangeFields: base, SignerAddress: "GSIGN", OldWeight: &oldW, NewWeight: newW},
			&types.SignerUpdatedChange{StateChangeBase: wantBase, SignerAddress: "GSIGN", OldWeight: &oldW, NewWeight: newW},
		},
		{
			"signer_removed", &wbtypes.SignerRemovedChange{BaseStateChangeFields: base, SignerAddress: "GSIGN", OldWeight: &oldW},
			&types.SignerRemovedChange{StateChangeBase: wantBase, SignerAddress: "GSIGN", OldWeight: &oldW},
		},
		{
			"threshold", &wbtypes.ThresholdChange{BaseStateChangeFields: base, OldThreshold: &oldW, NewThreshold: newW},
			&types.ThresholdChange{StateChangeBase: wantBase, OldThreshold: &oldW, NewThreshold: newW},
		},
		{
			"account_flags", &wbtypes.AccountFlagsChange{BaseStateChangeFields: base, Flags: []wbtypes.AccountFlag{wbtypes.AccountFlagAuthRequired}},
			&types.AccountFlagsChange{StateChangeBase: wantBase, Flags: []string{"AUTH_REQUIRED"}},
		},
		{
			"home_domain", &wbtypes.HomeDomainChange{BaseStateChangeFields: base, OldHomeDomain: &s, NewHomeDomain: &s},
			&types.HomeDomainChange{StateChangeBase: wantBase, OldHomeDomain: &s, NewHomeDomain: &s},
		},
		{
			"data_entry", &wbtypes.DataEntryChange{BaseStateChangeFields: base, Name: "k", OldValue: &s, NewValue: &s},
			&types.DataEntryChange{StateChangeBase: wantBase, Name: "k", OldValue: &s, NewValue: &s},
		},
		{
			"allowance", &wbtypes.AllowanceChange{BaseStateChangeFields: base, TokenID: "CTOKEN", Spender: "GSPEND", Amount: "5", ExpirationLedger: 42},
			&types.AllowanceChange{StateChangeBase: wantBase, TokenID: "CTOKEN", Spender: "GSPEND", Amount: "5", ExpirationLedger: 42},
		},
		{
			"trustline_added", &wbtypes.TrustlineAddedChange{BaseStateChangeFields: base, TokenID: &s, LiquidityPoolID: &s, Limit: "100"},
			&types.TrustlineAddedChange{StateChangeBase: wantBase, TokenID: &s, LiquidityPoolID: &s, Limit: "100"},
		},
		{
			"trustline_updated", &wbtypes.TrustlineUpdatedChange{BaseStateChangeFields: base, TokenID: &s, LiquidityPoolID: &s, OldLimit: "10", NewLimit: "20"},
			&types.TrustlineUpdatedChange{StateChangeBase: wantBase, TokenID: &s, LiquidityPoolID: &s, OldLimit: "10", NewLimit: "20"},
		},
		{
			"trustline_removed", &wbtypes.TrustlineRemovedChange{BaseStateChangeFields: base, TokenID: &s, LiquidityPoolID: &s},
			&types.TrustlineRemovedChange{StateChangeBase: wantBase, TokenID: &s, LiquidityPoolID: &s},
		},
		{
			"sponsorship", &wbtypes.SponsorshipChange{BaseStateChangeFields: base, SponsoredAddress: &s, SponsorAddress: &s, TokenID: &s, LiquidityPoolID: &s, ClaimableBalanceID: &s, DataName: &s, SignerAddress: &s},
			&types.SponsorshipChange{StateChangeBase: wantBase, SponsoredAddress: &s, SponsorAddress: &s, TokenID: &s, LiquidityPoolID: &s, ClaimableBalanceID: &s, DataName: &s, SignerAddress: &s},
		},
		{
			"balance_authorization", &wbtypes.BalanceAuthorizationChange{BaseStateChangeFields: base, TokenID: &s, LiquidityPoolID: &s, Flags: []wbtypes.TrustlineFlag{wbtypes.TrustlineFlagAuthorized}},
			&types.BalanceAuthorizationChange{StateChangeBase: wantBase, TokenID: &s, LiquidityPoolID: &s, Flags: []string{"AUTHORIZED"}},
		},
		{
			"balance_authorization_nil_flags", &wbtypes.BalanceAuthorizationChange{BaseStateChangeFields: base, TokenID: &s},
			&types.BalanceAuthorizationChange{StateChangeBase: wantBase, TokenID: &s},
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
		Operations:   []*wbtypes.Operation{{ID: 1, OperationType: wbtypes.OperationTypePayment}, nil},
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
