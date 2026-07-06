// ABOUTME: Wire-contract tests for the snake_case account-balances REST response types.
// ABOUTME: Asserts JSON key casing across all balance variants and the enriched envelope fields.
package types_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

func strPtr(s string) *string { return &s }

func TestAccountBalances_JSONWireContract(t *testing.T) {
	t.Parallel()
	ab := types.AccountBalances{
		Address:       "GADDRESS",
		IsFunded:      true,
		SubentryCount: 5,
		Balances: []types.Balance{
			&types.NativeBalance{
				BalanceBase:        types.BalanceBase{Balance: "100.0000000", Available: "97.5000000", TokenID: "native", TokenType: "NATIVE"},
				MinimumBalance:     "2.5000000",
				BuyingLiabilities:  "0.0000000",
				SellingLiabilities: "0.0000000",
				LastModifiedLedger: 100,
			},
			&types.TrustlineBalance{
				BalanceBase:                       types.BalanceBase{Balance: "50.0000000", Available: "40.0000000", TokenID: "USDC-GA5Z", TokenType: "CLASSIC"},
				Code:                              strPtr("USDC"),
				Issuer:                            strPtr("GA5Z"),
				Type:                              "credit_alphanum4",
				Limit:                             "1000.0000000",
				BuyingLiabilities:                 "0.0000000",
				SellingLiabilities:                "10.0000000",
				LastModifiedLedger:                200,
				IsAuthorized:                      true,
				IsAuthorizedToMaintainLiabilities: true,
			},
			&types.SACBalance{
				BalanceBase:       types.BalanceBase{Balance: "5000000000", Available: "5000000000", TokenID: "sac", TokenType: "SAC"},
				Code:              "USDC",
				Issuer:            "GA5Z",
				Decimals:          7,
				IsAuthorized:      true,
				IsClawbackEnabled: false,
			},
			&types.SEP41Balance{
				BalanceBase:        types.BalanceBase{Balance: "123456789", Available: "123456789", TokenID: "sep41", TokenType: "SEP41"},
				Symbol:             strPtr("ABC"),
				Name:               strPtr("Alphabet"),
				Decimals:           6,
				LastModifiedLedger: 300,
			},
			&types.LiquidityPoolBalance{
				BalanceBase:     types.BalanceBase{Balance: "10.0000000", Available: "10.0000000", TokenID: "pool-1", TokenType: "LIQUIDITY_POOL"},
				LiquidityPoolID: "pool-1",
				Reserves: []types.LiquidityPoolReserve{
					{Asset: "native", Amount: "100.0000000"},
					{Asset: "USDC-GA5Z", Amount: "200.0000000"},
				},
				LastModifiedLedger: 400,
			},
		},
	}
	b, err := json.Marshal(ab)
	require.NoError(t, err)
	s := string(b)

	// Envelope fields.
	assert.Contains(t, s, `"address":"GADDRESS"`)
	assert.Contains(t, s, `"is_funded":true`)
	assert.Contains(t, s, `"subentry_count":5`)
	assert.Contains(t, s, `"balances":`)

	// Shared + per-variant snake_case keys.
	for _, want := range []string{
		`"balance":`, `"available":`, `"token_id":`, `"token_type":`,
		`"minimum_balance":`, `"buying_liabilities":`, `"selling_liabilities":`, `"last_modified_ledger":`,
		`"code":`, `"issuer":`, `"limit":`, `"is_authorized":`, `"is_authorized_to_maintain_liabilities":`,
		`"decimals":`, `"is_clawback_enabled":`, `"symbol":`, `"name":`,
		`"liquidity_pool_id":`, `"reserves":`, `"asset":`, `"amount":`,
	} {
		assert.Contains(t, s, want, "expected snake_case key %s", want)
	}

	// No camelCase leaks from the SDK types.
	for _, forbidden := range []string{
		"tokenId", "tokenType", "minimumBalance", "buyingLiabilities", "sellingLiabilities",
		"lastModifiedLedger", "isAuthorized", "isAuthorizedToMaintainLiabilities", "isClawbackEnabled",
		"liquidityPoolId", "isFunded", "subentryCount",
	} {
		assert.NotContains(t, s, forbidden, "camelCase key %s must not leak", forbidden)
	}
}

func TestAccountBalances_EmptyBalancesMarshalsAsArray(t *testing.T) {
	t.Parallel()
	ab := types.AccountBalances{Address: "GADDRESS", Balances: []types.Balance{}}
	b, err := json.Marshal(ab)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"balances":[]`)
	assert.NotContains(t, s, `"balances":null`)
}
