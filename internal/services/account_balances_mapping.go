// ABOUTME: Maps wallet-backend SDK balance types into freighter snake_case REST response types.
// ABOUTME: mapBalance is a type switch over the 5 SDK balance variants, computing per-variant `available`.
package services

import (
	"github.com/stellar/go/amount"

	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// mapBalance dispatches an SDK balance to the matching freighter variant,
// building the shared BalanceBase (balance/token_id/token_type from the
// interface getters plus a per-variant `available`) and returning the embedding
// per-variant response type. The SDK's UnmarshalBalance rejects unknown __typename, so the
// default branch is unreachable in practice; it degrades a hypothetical future
// variant to the base fields rather than panicking or dropping the balance.
func mapBalance(b wbtypes.Balance) types.Balance {
	base := types.BalanceBase{
		Balance:   b.GetBalance(),
		TokenID:   b.GetTokenID(),
		TokenType: string(b.GetTokenType()),
	}
	switch bal := b.(type) {
	case *wbtypes.NativeBalance:
		// MinimumBalance already includes selling liabilities, so it is the only
		// amount subtracted here.
		base.Available = spendable(bal.BalanceValue, bal.MinimumBalance)
		return &types.NativeBalance{
			BalanceBase:        base,
			MinimumBalance:     bal.MinimumBalance,
			BuyingLiabilities:  bal.BuyingLiabilities,
			SellingLiabilities: bal.SellingLiabilities,
			LastModifiedLedger: bal.LastModifiedLedger,
		}
	case *wbtypes.TrustlineBalance:
		base.Available = spendable(bal.BalanceValue, bal.SellingLiabilities)
		return &types.TrustlineBalance{
			BalanceBase:                       base,
			Code:                              bal.Code,
			Issuer:                            bal.Issuer,
			Type:                              bal.Type,
			Limit:                             bal.Limit,
			BuyingLiabilities:                 bal.BuyingLiabilities,
			SellingLiabilities:                bal.SellingLiabilities,
			LastModifiedLedger:                bal.LastModifiedLedger,
			IsAuthorized:                      bal.IsAuthorized,
			IsAuthorizedToMaintainLiabilities: bal.IsAuthorizedToMaintainLiabilities,
		}
	case *wbtypes.SACBalance:
		// SAC balances are raw i128 amounts with no liabilities, so the full
		// balance is spendable.
		base.Available = base.Balance
		return &types.SACBalance{
			BalanceBase:       base,
			Code:              bal.Code,
			Issuer:            bal.Issuer,
			Decimals:          bal.Decimals,
			IsAuthorized:      bal.IsAuthorized,
			IsClawbackEnabled: bal.IsClawbackEnabled,
		}
	case *wbtypes.SEP41Balance:
		// SEP-41 balances are raw i128 amounts with no liabilities, so the full
		// balance is spendable.
		base.Available = base.Balance
		return &types.SEP41Balance{
			BalanceBase:        base,
			Symbol:             bal.Symbol,
			Name:               bal.Name,
			Decimals:           bal.Decimals,
			LastModifiedLedger: bal.LastModifiedLedger,
		}
	case *wbtypes.LiquidityPoolBalance:
		base.Available = base.Balance
		reserves := make([]types.LiquidityPoolReserve, 0, len(bal.Reserves))
		for _, r := range bal.Reserves {
			reserves = append(reserves, types.LiquidityPoolReserve{Asset: r.Asset, Amount: r.Amount})
		}
		return &types.LiquidityPoolBalance{
			BalanceBase:        base,
			LiquidityPoolID:    bal.LiquidityPoolID,
			Reserves:           reserves,
			LastModifiedLedger: bal.LastModifiedLedger,
		}
	default:
		return &base
	}
}

// spendable returns balance minus reserved, clamped at zero, as a Stellar amount
// string. balance and reserved are pre-formatted Stellar amount strings (7
// decimal places); if either fails to parse it falls back to the raw balance.
func spendable(balance, reserved string) string {
	bal, balErr := amount.ParseInt64(balance)
	res, resErr := amount.ParseInt64(reserved)
	if balErr != nil || resErr != nil {
		return balance
	}
	if avail := bal - res; avail > 0 {
		return amount.StringFromInt64(avail)
	}
	return amount.StringFromInt64(0)
}
