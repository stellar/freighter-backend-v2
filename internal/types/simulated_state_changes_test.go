// ABOUTME: Wire-contract test for the simulate-state-changes response types.
package types_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

func TestSimulatedStateChanges_JSONWireContract(t *testing.T) {
	t.Parallel()
	opIndex := int32(0)
	code, issuer := "USDC", "GISSUER"
	result := types.SimulatedStateChanges{
		LatestLedger: 123,
		StateChanges: []types.SimulatedStateChange{
			&types.SimulatedBalanceChange{
				SimulatedStateChangeBase: types.SimulatedStateChangeBase{Variant: "BalanceChange", Type: "BALANCE", Reason: "DEBIT", AccountAddress: "GSENDER", OperationIndex: &opIndex},
				TokenID:                  "CUSDC", Amount: "5000000",
			},
		},
		Tokens: []types.SimulatedToken{{TokenID: "CUSDC", Code: &code, Issuer: &issuer, Decimals: 7}},
	}

	b, err := json.Marshal(result)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"latest_ledger": 123,
		"state_changes": [
			{"variant": "BalanceChange", "type": "BALANCE", "reason": "DEBIT", "account_address": "GSENDER", "operation_index": 0, "token_id": "CUSDC", "amount": "5000000"}
		],
		"tokens": [{"token_id": "CUSDC", "code": "USDC", "issuer": "GISSUER", "decimals": 7}]
	}`, string(b))
}
