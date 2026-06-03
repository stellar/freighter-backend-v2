// ABOUTME: Unit tests for the wbclient -> freighter snake_case mapping helpers.
// ABOUTME: Covers transaction/operation field mapping, all 9 state-change variants, and edge flattening.
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
	base := wbtypes.BaseStateChangeFields{Type: wbtypes.StateChangeCategoryBalance, Reason: wbtypes.StateChangeReasonDebit}
	s := "x"
	cases := []struct {
		name string
		in   wbtypes.StateChangeNode
		want types.StateChange
	}{
		{
			"standard_balance", &wbtypes.StandardBalanceChange{BaseStateChangeFields: base, TokenID: "native", Amount: "10"},
			&types.StandardBalanceChange{StateChangeBase: types.StateChangeBase{Type: "BALANCE", Reason: "DEBIT"}, StandardBalanceTokenID: "native", Amount: "10"},
		},
		{
			"account", &wbtypes.AccountChange{BaseStateChangeFields: base, FunderAddress: &s},
			&types.AccountChange{StateChangeBase: types.StateChangeBase{Type: "BALANCE", Reason: "DEBIT"}, FunderAddress: &s},
		},
		{
			"signer", &wbtypes.SignerChange{BaseStateChangeFields: base, SignerAddress: &s, SignerWeights: &s},
			&types.SignerChange{StateChangeBase: types.StateChangeBase{Type: "BALANCE", Reason: "DEBIT"}, SignerAddress: &s, SignerWeights: &s},
		},
		{
			"signer_thresholds", &wbtypes.SignerThresholdsChange{BaseStateChangeFields: base, Thresholds: "1,2,3"},
			&types.SignerThresholdsChange{StateChangeBase: types.StateChangeBase{Type: "BALANCE", Reason: "DEBIT"}, Thresholds: "1,2,3"},
		},
		{
			"metadata", &wbtypes.MetadataChange{BaseStateChangeFields: base, KeyValue: "k=v"},
			&types.MetadataChange{StateChangeBase: types.StateChangeBase{Type: "BALANCE", Reason: "DEBIT"}, MetadataKeyValue: "k=v"},
		},
		{
			"flags", &wbtypes.FlagsChange{BaseStateChangeFields: base, Flags: []string{"AUTH"}},
			&types.FlagsChange{StateChangeBase: types.StateChangeBase{Type: "BALANCE", Reason: "DEBIT"}, Flags: []string{"AUTH"}},
		},
		{
			"trustline", &wbtypes.TrustlineChange{BaseStateChangeFields: base, TokenID: &s, Limit: &s, LiquidityPoolID: &s},
			&types.TrustlineChange{StateChangeBase: types.StateChangeBase{Type: "BALANCE", Reason: "DEBIT"}, TrustlineTokenID: &s, Limit: &s, TrustlineLiquidityPoolID: &s},
		},
		{
			"reserves", &wbtypes.ReservesChange{BaseStateChangeFields: base, SponsoredAddress: &s, SponsorAddress: &s, SponsoredData: &s, SponsoredTrustline: &s, ClaimableBalanceID: &s, LiquidityPoolID: &s},
			&types.ReservesChange{StateChangeBase: types.StateChangeBase{Type: "BALANCE", Reason: "DEBIT"}, SponsoredAddress: &s, SponsorAddress: &s, SponsoredData: &s, SponsoredTrustline: &s, ClaimableBalanceID: &s, LiquidityPoolID: &s},
		},
		{
			"balance_authorization", &wbtypes.BalanceAuthorizationChange{BaseStateChangeFields: base, TokenID: &s, LiquidityPoolID: &s, Flags: []string{"X"}},
			&types.BalanceAuthorizationChange{StateChangeBase: types.StateChangeBase{Type: "BALANCE", Reason: "DEBIT"}, BalanceAuthTokenID: &s, BalanceAuthLiquidityPoolID: &s, Flags: []string{"X"}},
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
		StateChanges: []wbtypes.StateChangeNode{&wbtypes.StandardBalanceChange{BaseStateChangeFields: wbtypes.BaseStateChangeFields{Type: wbtypes.StateChangeCategoryBalance}, Amount: "5"}, nil},
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
