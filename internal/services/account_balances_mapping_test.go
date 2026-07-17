// ABOUTME: Unit tests for the wbclient -> freighter snake_case balance mapping helpers.
// ABOUTME: Covers all 5 balance variants, the per-variant `available` computation, and snake_case JSON tags.
package services

import (
	"testing"

	"github.com/stretchr/testify/assert"

	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

func strPtr(s string) *string { return &s }

func TestMapBalance_AllVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   wbtypes.Balance
		want types.Balance
	}{
		{
			// available = balance - minimum_balance - selling_liabilities = 100 - 1.3 - 0.2 = 98.5.
			// minimum_balance is the pure reserve; selling liabilities are a separate subtrahend, so
			// this case proves both are netted (subtracting only one would give 98.7 or 99.8).
			"native", &wbtypes.NativeBalance{
				BalanceValue: "100.0000000", TokenID: "native", TokenType: wbtypes.TokenTypeNative,
				MinimumBalance: "1.3000000", BuyingLiabilities: "0.5000000", SellingLiabilities: "0.2000000",
				LastModifiedLedger: 100, NumSubentries: 3,
			},
			&types.NativeBalance{
				BalanceBase: types.BalanceBase{
					Key:   "native",
					Token: &types.Token{Type: "native", Code: "XLM"},
					Total: "100.0000000", Available: "98.5000000", TokenID: "native", TokenType: "NATIVE",
				},
				MinimumBalance:     "1.3000000",
				BuyingLiabilities:  "0.5000000",
				SellingLiabilities: "0.2000000",
				LastModifiedLedger: 100,
			},
		},
		{
			// Underfunded native: balance < minimum_balance + selling_liabilities clamps available to 0.
			"native_underfunded", &wbtypes.NativeBalance{
				BalanceValue: "1.0000000", TokenID: "native", TokenType: wbtypes.TokenTypeNative,
				MinimumBalance: "0.8000000", BuyingLiabilities: "0.0000000", SellingLiabilities: "0.5000000",
				LastModifiedLedger: 42, NumSubentries: 0,
			},
			&types.NativeBalance{
				BalanceBase: types.BalanceBase{
					Key:   "native",
					Token: &types.Token{Type: "native", Code: "XLM"},
					Total: "1.0000000", Available: "0.0000000", TokenID: "native", TokenType: "NATIVE",
				},
				MinimumBalance:     "0.8000000",
				BuyingLiabilities:  "0.0000000",
				SellingLiabilities: "0.5000000",
				LastModifiedLedger: 42,
			},
		},
		{
			// available = balance - sellingLiabilities = 50 - 10 = 40.
			"trustline", &wbtypes.TrustlineBalance{
				BalanceValue: "50.0000000", TokenID: "USDC-GA5Z", TokenType: wbtypes.TokenTypeClassic,
				Code: strPtr("USDC"), Issuer: strPtr("GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"),
				Type: "credit_alphanum4", Limit: "922337203685.4775807",
				BuyingLiabilities: "1.0000000", SellingLiabilities: "10.0000000",
				LastModifiedLedger: 200, IsAuthorized: true, IsAuthorizedToMaintainLiabilities: true,
			},
			&types.TrustlineBalance{
				BalanceBase: types.BalanceBase{
					Key: "USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
					Token: &types.Token{
						Type: "credit_alphanum4", Code: "USDC",
						Issuer: &types.TokenIssuer{Key: "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
					},
					Total: "50.0000000", Available: "40.0000000", TokenID: "USDC-GA5Z", TokenType: "CLASSIC",
				},
				Code:                              strPtr("USDC"),
				Issuer:                            strPtr("GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"),
				Type:                              "credit_alphanum4",
				Limit:                             "922337203685.4775807",
				BuyingLiabilities:                 "1.0000000",
				SellingLiabilities:                "10.0000000",
				LastModifiedLedger:                200,
				IsAuthorized:                      true,
				IsAuthorizedToMaintainLiabilities: true,
			},
		},
		{
			// Underfunded trustline: balance < sellingLiabilities clamps to 0.
			"trustline_underfunded", &wbtypes.TrustlineBalance{
				BalanceValue: "5.0000000", TokenID: "USDC-GA5Z", TokenType: wbtypes.TokenTypeClassic,
				Code: strPtr("USDC"), Issuer: strPtr("GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"),
				Type: "credit_alphanum4", Limit: "1000.0000000",
				BuyingLiabilities: "0.0000000", SellingLiabilities: "9.0000000",
				LastModifiedLedger: 201, IsAuthorized: true, IsAuthorizedToMaintainLiabilities: false,
			},
			&types.TrustlineBalance{
				BalanceBase: types.BalanceBase{
					Key: "USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
					Token: &types.Token{
						Type: "credit_alphanum4", Code: "USDC",
						Issuer: &types.TokenIssuer{Key: "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
					},
					Total: "5.0000000", Available: "0.0000000", TokenID: "USDC-GA5Z", TokenType: "CLASSIC",
				},
				Code:                              strPtr("USDC"),
				Issuer:                            strPtr("GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"),
				Type:                              "credit_alphanum4",
				Limit:                             "1000.0000000",
				BuyingLiabilities:                 "0.0000000",
				SellingLiabilities:                "9.0000000",
				LastModifiedLedger:                201,
				IsAuthorized:                      true,
				IsAuthorizedToMaintainLiabilities: false,
			},
		},
		{
			// SAC: available == balance (raw i128, no liabilities, no arithmetic).
			"sac", &wbtypes.SACBalance{
				BalanceValue: "5000000000", TokenID: "USDC-GA5Z:contract", TokenType: wbtypes.TokenTypeSAC,
				Code: "USDC", Issuer: "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
				Decimals: 7, IsAuthorized: true, IsClawbackEnabled: false,
			},
			&types.SACBalance{
				BalanceBase: types.BalanceBase{
					Key: "USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
					Token: &types.Token{
						Type: "credit_alphanum4", Code: "USDC",
						Issuer: &types.TokenIssuer{Key: "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
					},
					Total: "5000000000", Available: "5000000000", TokenID: "USDC-GA5Z:contract", TokenType: "SAC",
				},
				Code:              "USDC",
				Issuer:            "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
				Decimals:          7,
				IsAuthorized:      true,
				IsClawbackEnabled: false,
			},
		},
		{
			// SEP41: available == balance (raw i128, plain string copy).
			"sep41", &wbtypes.SEP41Balance{
				BalanceValue: "123456789", TokenID: "CBSEP41CONTRACT", TokenType: wbtypes.TokenTypeSEP41,
				Symbol: strPtr("ABC"), Name: strPtr("Alphabet"), Decimals: 6, LastModifiedLedger: 300,
			},
			&types.SEP41Balance{
				BalanceBase: types.BalanceBase{
					Key:   "ABC:CBSEP41CONTRACT",
					Token: &types.Token{Code: "ABC", Issuer: &types.TokenIssuer{Key: "CBSEP41CONTRACT"}},
					Total: "123456789", Available: "123456789", TokenID: "CBSEP41CONTRACT", TokenType: "SEP41",
				},
				Symbol:             strPtr("ABC"),
				Name:               strPtr("Alphabet"),
				Decimals:           6,
				LastModifiedLedger: 300,
			},
		},
		{
			// LP: available == balance (pool shares, plain string copy).
			"liquidity_pool", &wbtypes.LiquidityPoolBalance{
				BalanceValue: "10.0000000", TokenID: "pool-1", TokenType: wbtypes.TokenTypeLiquidityPool,
				LiquidityPoolID: "pool-1",
				Reserves: []wbtypes.LiquidityPoolReserve{
					{Asset: "native", Amount: "100.0000000"},
					{Asset: "USDC-GA5Z", Amount: "200.0000000"},
				},
				LastModifiedLedger: 400,
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
		{
			// SAC with a >4-char code derives credit_alphanum12 (no Type on the SDK
			// SAC variant, so the mapper derives it from code length like v1 does).
			"sac_alphanum12", &wbtypes.SACBalance{
				BalanceValue: "1", TokenID: "TOKEN-GA5Z:contract", TokenType: wbtypes.TokenTypeSAC,
				Code: "TOKEN", Issuer: "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
				Decimals: 7, IsAuthorized: true, IsClawbackEnabled: false,
			},
			&types.SACBalance{
				BalanceBase: types.BalanceBase{
					Key: "TOKEN:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
					Token: &types.Token{
						Type: "credit_alphanum12", Code: "TOKEN",
						Issuer: &types.TokenIssuer{Key: "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
					},
					Total: "1", Available: "1", TokenID: "TOKEN-GA5Z:contract", TokenType: "SAC",
				},
				Code:         "TOKEN",
				Issuer:       "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
				Decimals:     7,
				IsAuthorized: true,
			},
		},
		{
			// Nil code/issuer (the SDK models them as optional): key degrades to
			// ":" with empty token fields rather than panicking.
			"trustline_nil_code_issuer", &wbtypes.TrustlineBalance{
				BalanceValue: "1.0000000", TokenID: "tl", TokenType: wbtypes.TokenTypeClassic,
				Type: "credit_alphanum4", Limit: "10.0000000",
				BuyingLiabilities: "0.0000000", SellingLiabilities: "0.0000000",
				LastModifiedLedger: 500,
			},
			&types.TrustlineBalance{
				BalanceBase: types.BalanceBase{
					Key:   ":",
					Token: &types.Token{Type: "credit_alphanum4", Code: "", Issuer: &types.TokenIssuer{Key: ""}},
					Total: "1.0000000", Available: "1.0000000", TokenID: "tl", TokenType: "CLASSIC",
				},
				Type:               "credit_alphanum4",
				Limit:              "10.0000000",
				BuyingLiabilities:  "0.0000000",
				SellingLiabilities: "0.0000000",
				LastModifiedLedger: 500,
			},
		},
		{
			// Nil symbol: key degrades to ":CONTRACT" with an empty token code,
			// mirroring the client-side fallback this mapping replaces.
			"sep41_nil_symbol", &wbtypes.SEP41Balance{
				BalanceValue: "42", TokenID: "CBSEP41CONTRACT", TokenType: wbtypes.TokenTypeSEP41,
				Decimals: 6, LastModifiedLedger: 600,
			},
			&types.SEP41Balance{
				BalanceBase: types.BalanceBase{
					Key:   ":CBSEP41CONTRACT",
					Token: &types.Token{Code: "", Issuer: &types.TokenIssuer{Key: "CBSEP41CONTRACT"}},
					Total: "42", Available: "42", TokenID: "CBSEP41CONTRACT", TokenType: "SEP41",
				},
				Decimals:           6,
				LastModifiedLedger: 600,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, mapBalance(tc.in))
		})
	}
}
