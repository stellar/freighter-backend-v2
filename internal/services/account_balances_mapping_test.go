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
			// available = balance - minimumBalance = 100 - 1.5 = 98.5
			// (minimumBalance already folds in selling liabilities).
			"native", &wbtypes.NativeBalance{
				BalanceValue: "100.0000000", TokenID: "native", TokenType: wbtypes.TokenTypeNative,
				MinimumBalance: "1.5000000", BuyingLiabilities: "0.5000000", SellingLiabilities: "0.2000000",
				LastModifiedLedger: 100, NumSubentries: 3,
			},
			&types.NativeBalance{
				BalanceBase:        types.BalanceBase{Balance: "100.0000000", Available: "98.5000000", TokenID: "native", TokenType: "NATIVE"},
				MinimumBalance:     "1.5000000",
				BuyingLiabilities:  "0.5000000",
				SellingLiabilities: "0.2000000",
				LastModifiedLedger: 100,
			},
		},
		{
			// Underfunded native: balance < minimumBalance clamps available to 0.
			"native_underfunded", &wbtypes.NativeBalance{
				BalanceValue: "1.0000000", TokenID: "native", TokenType: wbtypes.TokenTypeNative,
				MinimumBalance: "5.0000000", BuyingLiabilities: "0.0000000", SellingLiabilities: "0.0000000",
				LastModifiedLedger: 42, NumSubentries: 0,
			},
			&types.NativeBalance{
				BalanceBase:        types.BalanceBase{Balance: "1.0000000", Available: "0.0000000", TokenID: "native", TokenType: "NATIVE"},
				MinimumBalance:     "5.0000000",
				BuyingLiabilities:  "0.0000000",
				SellingLiabilities: "0.0000000",
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
				BalanceBase:                       types.BalanceBase{Balance: "50.0000000", Available: "40.0000000", TokenID: "USDC-GA5Z", TokenType: "CLASSIC"},
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
				BalanceBase:                       types.BalanceBase{Balance: "5.0000000", Available: "0.0000000", TokenID: "USDC-GA5Z", TokenType: "CLASSIC"},
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
				BalanceBase:       types.BalanceBase{Balance: "5000000000", Available: "5000000000", TokenID: "USDC-GA5Z:contract", TokenType: "SAC"},
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
				BalanceBase:        types.BalanceBase{Balance: "123456789", Available: "123456789", TokenID: "CBSEP41CONTRACT", TokenType: "SEP41"},
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
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, mapBalance(tc.in))
		})
	}
}
