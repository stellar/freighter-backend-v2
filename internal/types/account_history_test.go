// ABOUTME: Wire-contract tests for the snake_case account-history REST response types.
// ABOUTME: Asserts JSON key casing, string-encoded 64-bit ints, and polymorphic state-change shape.
package types_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

func TestAccountTransaction_JSONWireContract(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	creator := "GFUNDER"
	at := types.AccountTransaction{
		Transaction: types.Transaction{
			Hash: "h1", FeeCharged: 100, ResultCode: "txSUCCESS",
			LedgerNumber: 51234567, LedgerCreatedAt: ts, IsFeeBump: false, IngestedAt: ts,
		},
		Operations: []types.Operation{{
			ID: 220000000000000, OperationType: "PAYMENT", OperationXDR: "AAA",
			ResultCode: "opSUCCESS", Successful: true, LedgerNumber: 51234567,
			LedgerCreatedAt: ts, IngestedAt: ts,
		}},
		StateChanges: []types.StateChange{
			&types.BalanceChange{
				StateChangeBase: types.StateChangeBase{Variant: "BalanceChange", Type: "BALANCE", Reason: "DEBIT", LedgerNumber: 51234567, LedgerCreatedAt: ts, IngestedAt: ts},
				TokenID:         "native", Amount: "10.0000000",
			},
			&types.AccountCreatedChange{
				StateChangeBase: types.StateChangeBase{Variant: "AccountCreatedChange", Type: "ACCOUNT", Reason: "CREATE", LedgerNumber: 51234567, LedgerCreatedAt: ts, IngestedAt: ts},
				CreatorAddress:  creator,
			},
			&types.AccountFlagsChange{
				StateChangeBase: types.StateChangeBase{Variant: "AccountFlagsChange", Type: "FLAGS", Reason: "SET", LedgerNumber: 51234567, LedgerCreatedAt: ts, IngestedAt: ts},
				Flags:           []string{},
			},
		},
	}
	b, err := json.Marshal(at)
	require.NoError(t, err)
	s := string(b)

	assert.Contains(t, s, `"fee_charged":"100"`)
	assert.Contains(t, s, `"id":"220000000000000"`)
	assert.Contains(t, s, `"ledger_number":51234567`)
	assert.Contains(t, s, `"result_code":"txSUCCESS"`)
	assert.Contains(t, s, `"operation_type":"PAYMENT"`)
	assert.Contains(t, s, `"operation_xdr":"AAA"`)
	assert.NotContains(t, s, "feeCharged")
	assert.NotContains(t, s, "operationType")
	assert.NotContains(t, s, "ledgerCreatedAt")
	assert.Contains(t, s, `"state_changes":`)
	assert.Contains(t, s, `"variant":"BalanceChange"`)
	assert.Contains(t, s, `"type":"BALANCE"`)
	assert.Contains(t, s, `"token_id":"native"`)
	assert.Contains(t, s, `"amount":"10.0000000"`)
	assert.Contains(t, s, `"variant":"AccountCreatedChange"`)
	assert.Contains(t, s, `"type":"ACCOUNT"`)
	assert.Contains(t, s, `"creator_address":"GFUNDER"`)
	// The upstream flags list is non-null, so an empty AccountFlagsChange
	// encodes an empty array rather than null.
	assert.Contains(t, s, `"flags":[]`)
	assert.NotContains(t, s, `"flags":null`)
}

// TestBalanceAuthorizationChange_EmptyFlagsOmitsKey is the counterpart to the
// AccountFlagsChange assertions above, covering the other side of upstream's
// nullability split: BalanceAuthorizationChange.flags is `[TrustlineFlag!]`
// (nullable — null for SAC contract-holder authorization, which has no trustline
// flags), so the REST field carries omitempty and the key must disappear rather
// than encode [] or null.
//
// This also pins the behaviour the shared enumStrings mapper relies on:
// encoding/json omits any zero-length slice, so the mapper is free to return a
// non-nil empty slice here without changing the wire.
func TestBalanceAuthorizationChange_EmptyFlagsOmitsKey(t *testing.T) {
	t.Parallel()
	token := "USDC-GISSUER"
	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"non_nil_empty", []string{}},
		{"nil", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(&types.BalanceAuthorizationChange{
				StateChangeBase: types.StateChangeBase{Variant: "BalanceAuthorizationChange", Type: "BALANCE", Reason: "SET"},
				TokenID:         &token,
				Flags:           tc.flags,
			})
			require.NoError(t, err)
			s := string(b)
			assert.NotContains(t, s, `"flags"`, "omitempty drops the key entirely for a zero-length slice")
			assert.NotContains(t, s, `"flags":[]`)
			assert.NotContains(t, s, `"flags":null`)
			// The rest of the variant still encodes normally.
			assert.Contains(t, s, `"variant":"BalanceAuthorizationChange"`)
			assert.Contains(t, s, `"token_id":"USDC-GISSUER"`)
		})
	}
}

// TestBalanceAuthorizationChange_PopulatedFlagsEncode guards the opposite
// direction: a classic-trustline authorization must still emit its flags.
func TestBalanceAuthorizationChange_PopulatedFlagsEncode(t *testing.T) {
	t.Parallel()
	pool := "POOL1"
	b, err := json.Marshal(&types.BalanceAuthorizationChange{
		StateChangeBase: types.StateChangeBase{Variant: "BalanceAuthorizationChange", Type: "BALANCE", Reason: "SET"},
		LiquidityPoolID: &pool,
		Flags:           []string{"AUTHORIZED", "CLAWBACK_ENABLED"},
	})
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"flags":["AUTHORIZED","CLAWBACK_ENABLED"]`)
	assert.Contains(t, s, `"liquidity_pool_id":"POOL1"`)
	assert.NotContains(t, s, `"token_id"`, "omitempty drops the unset alternative")
}

func TestAccountTransaction_EmptyDetailsMarshalAsArrays(t *testing.T) {
	t.Parallel()
	at := types.AccountTransaction{
		Transaction:  types.Transaction{Hash: "h1"},
		Operations:   []types.Operation{},
		StateChanges: []types.StateChange{},
	}
	b, err := json.Marshal(at)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"operations":[]`)
	assert.Contains(t, s, `"state_changes":[]`)
}
