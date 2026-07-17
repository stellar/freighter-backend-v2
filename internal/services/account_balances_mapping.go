// ABOUTME: Maps wallet-backend SDK balance types into freighter snake_case REST response types.
// ABOUTME: mapBalance is a type switch over the 5 SDK balance variants, deriving the v1-format key/token per variant.
package services

import (
	"github.com/stellar/go/amount"

	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// mapBalance dispatches an SDK balance to the matching freighter variant,
// building the shared BalanceBase (total/token_id/token_type from the
// interface getters plus per-variant `available`, `key`, and `token`) and
// returning the embedding per-variant response type. Key and Token follow the
// v1 balance-map conventions so clients index balances without re-deriving
// them (issue stellar/freighter-backend#319). The SDK's UnmarshalBalance
// rejects unknown __typename, so the default branch is unreachable in
// practice; it degrades a hypothetical future variant to the base fields
// (key = token_id, no token) rather than panicking or dropping the balance.
func mapBalance(b wbtypes.Balance) types.Balance {
	base := types.BalanceBase{
		Key:       b.GetTokenID(),
		Total:     b.GetBalance(),
		TokenID:   b.GetTokenID(),
		TokenType: string(b.GetTokenType()),
	}
	switch bal := b.(type) {
	case *wbtypes.NativeBalance:
		base.Key = "native"
		base.Token = &types.Token{Type: "native", Code: "XLM"}
		// MinimumBalance is the pure base reserve; selling liabilities also lock XLM,
		// so both are subtracted (mirrors stellar-core getAvailableBalance).
		base.Available = spendable(bal.BalanceValue, bal.MinimumBalance, bal.SellingLiabilities)
		return &types.NativeBalance{
			BalanceBase:        base,
			MinimumBalance:     bal.MinimumBalance,
			BuyingLiabilities:  bal.BuyingLiabilities,
			SellingLiabilities: bal.SellingLiabilities,
			LastModifiedLedger: bal.LastModifiedLedger,
		}
	case *wbtypes.TrustlineBalance:
		code, issuer := deref(bal.Code), deref(bal.Issuer)
		base.Key = code + ":" + issuer
		// The SDK carries the trustline's asset type verbatim (e.g.
		// credit_alphanum4), so no derivation is needed here.
		base.Token = &types.Token{Type: bal.Type, Code: code, Issuer: &types.TokenIssuer{Key: issuer}}
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
		base.Key = bal.Code + ":" + bal.Issuer
		base.Token = &types.Token{Type: classicAssetType(bal.Code), Code: bal.Code, Issuer: &types.TokenIssuer{Key: bal.Issuer}}
		// SAC balances are raw i128 amounts with no liabilities, so the full
		// balance is spendable.
		base.Available = base.Total
		return &types.SACBalance{
			BalanceBase:       base,
			Code:              bal.Code,
			Issuer:            bal.Issuer,
			Decimals:          bal.Decimals,
			IsAuthorized:      bal.IsAuthorized,
			IsClawbackEnabled: bal.IsClawbackEnabled,
		}
	case *wbtypes.SEP41Balance:
		symbol := deref(bal.Symbol)
		base.Key = symbol + ":" + bal.TokenID
		// v1 parity: a pure SEP-41 token has no classic asset type, so Token
		// carries only the symbol and the contract id as the issuer key.
		base.Token = &types.Token{Code: symbol, Issuer: &types.TokenIssuer{Key: bal.TokenID}}
		// SEP-41 balances are raw i128 amounts with no liabilities, so the full
		// balance is spendable.
		base.Available = base.Total
		return &types.SEP41Balance{
			BalanceBase:        base,
			Symbol:             bal.Symbol,
			Name:               bal.Name,
			Decimals:           bal.Decimals,
			LastModifiedLedger: bal.LastModifiedLedger,
		}
	case *wbtypes.LiquidityPoolBalance:
		// v1 parity: LP share entries are keyed "<poolId>:lp" and carry no token.
		base.Key = bal.LiquidityPoolID + ":lp"
		base.Available = base.Total
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

// classicAssetType derives the classic asset type from the code length for
// variants where the SDK does not carry it (SAC), mirroring v1 and the
// clients' fallback: codes longer than 4 characters are credit_alphanum12.
func classicAssetType(code string) string {
	if len(code) > 4 {
		return "credit_alphanum12"
	}
	return "credit_alphanum4"
}

// deref returns the pointed-to string, or "" for nil — the SDK models
// optional codes/issuers/symbols as *string.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// spendable returns balance minus the sum of the reserved amounts, clamped at
// zero, as a Stellar amount string. All inputs are pre-formatted Stellar amount
// strings (7 decimal places); if any fails to parse it falls back to the raw
// balance.
func spendable(balance string, reserved ...string) string {
	bal, err := amount.ParseInt64(balance)
	if err != nil {
		return balance
	}
	var res int64
	for _, r := range reserved {
		v, err := amount.ParseInt64(r)
		if err != nil {
			return balance
		}
		res += v
	}
	if avail := bal - res; avail > 0 {
		return amount.StringFromInt64(avail)
	}
	return amount.StringFromInt64(0)
}
