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
				BalanceBase: types.BalanceBase{
					Key:   "native",
					Token: &types.Token{Type: "native", Code: "XLM"},
					Total: "100.0000000", Available: "97.5000000", TokenID: "native", TokenType: "NATIVE",
				},
				MinimumBalance:     "2.5000000",
				BuyingLiabilities:  "0.0000000",
				SellingLiabilities: "0.0000000",
				LastModifiedLedger: 100,
			},
			&types.TrustlineBalance{
				BalanceBase: types.BalanceBase{
					Key:   "USDC:GA5Z",
					Token: &types.Token{Type: "credit_alphanum4", Code: "USDC", Issuer: &types.TokenIssuer{Key: "GA5Z"}},
					Total: "50.0000000", Available: "40.0000000", TokenID: "USDC-GA5Z", TokenType: "CLASSIC",
				},
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
				BalanceBase: types.BalanceBase{
					Key:   "USDC:GA5Z",
					Token: &types.Token{Type: "credit_alphanum4", Code: "USDC", Issuer: &types.TokenIssuer{Key: "GA5Z"}},
					Total: "5000000000", Available: "5000000000", TokenID: "sac", TokenType: "SAC",
				},
				Code:              "USDC",
				Issuer:            "GA5Z",
				Decimals:          7,
				IsAuthorized:      true,
				IsClawbackEnabled: false,
			},
			&types.SEP41Balance{
				BalanceBase: types.BalanceBase{
					Key:   "ABC:sep41",
					Token: &types.Token{Code: "ABC", Issuer: &types.TokenIssuer{Key: "sep41"}},
					Total: "123456789", Available: "123456789", TokenID: "sep41", TokenType: "SEP41",
				},
				Symbol:             strPtr("ABC"),
				Name:               strPtr("Alphabet"),
				Decimals:           6,
				LastModifiedLedger: 300,
			},
			&types.LiquidityPoolBalance{
				BalanceBase: types.BalanceBase{
					Key:   "pool-1:lp",
					Total: "10.0000000", Available: "10.0000000", TokenID: "pool-1", TokenType: "LIQUIDITY_POOL",
				},
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
		`"key":`, `"token":`, `"total":`, `"available":`, `"token_id":`, `"token_type":`,
		`"minimum_balance":`, `"buying_liabilities":`, `"selling_liabilities":`, `"last_modified_ledger":`,
		`"code":`, `"issuer":`, `"limit":`, `"is_authorized":`, `"is_authorized_to_maintain_liabilities":`,
		`"decimals":`, `"is_clawback_enabled":`, `"symbol":`, `"name":`,
		`"liquidity_pool_id":`, `"reserves":`, `"asset":`, `"amount":`,
	} {
		assert.Contains(t, s, want, "expected snake_case key %s", want)
	}

	// v1 alignment: the on-ledger amount is exposed as "total", never "balance".
	// (Safe probe: the envelope's `"balances":` does not contain the substring `"balance":`.)
	assert.NotContains(t, s, `"balance":`, `on-ledger amount must be keyed "total" (v1 parity)`)

	// v1 token shapes: nested issuer object; native token has no issuer.
	assert.Contains(t, s, `"token":{"type":"native","code":"XLM"}`)
	assert.Contains(t, s, `"token":{"type":"credit_alphanum4","code":"USDC","issuer":{"key":"GA5Z"}}`)

	// SEP-41 token carries no "type" (v1 Mercury parity): code + issuer only.
	sep41, err := json.Marshal(ab.Balances[3])
	require.NoError(t, err)
	assert.Contains(t, string(sep41), `"token":{"code":"ABC","issuer":{"key":"sep41"}}`)

	// Liquidity-pool entries carry a key but no token object (v1 parity).
	// (Safe probe: `"token":` does not match "token_id"/"token_type".)
	lp, err := json.Marshal(ab.Balances[4])
	require.NoError(t, err)
	assert.Contains(t, string(lp), `"key":"pool-1:lp"`)
	assert.NotContains(t, string(lp), `"token":`)

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
