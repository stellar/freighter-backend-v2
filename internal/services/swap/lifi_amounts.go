package swap

import (
	"math/big"

	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// LifiHop is one entry of a step's estimate.hops: a quote-time estimate, not
// part of the transaction.
type LifiHop struct {
	Venue     string `json:"venue"`
	Path      int    `json:"path"`
	FromToken struct {
		Address string `json:"address"`
	} `json:"fromToken"`
	ToToken struct {
		Address string `json:"address"`
	} `json:"toToken"`
	FromAmount string `json:"fromAmount"`
	ToAmount   string `json:"toAmount"`
}

var lifiHopVenues = map[string]string{"Aquarius": "Aquarius", "Sushi": "SushiSwap", "Soroswap": "Soroswap"}

// lifiHopAmounts returns each hop's raw output amount, aligned with paths, or
// nil and the reason. Paths come from the signed transaction, so an estimate
// is used only when its hops match them hop for hop and its split matches
// their shares; otherwise the route is shown without amounts rather than with
// numbers for a route not being signed.
// Call it before labelSACHops: it compares contract IDs.
func lifiHopAmounts(q *LifiQuote, paths []types.SwapPath, label func(string) string) ([][]*big.Int, string) {
	var hops []LifiHop
	for _, s := range q.IncludedSteps {
		hops = append(hops, s.Estimate.Hops...)
	}
	if len(hops) == 0 {
		return nil, "missing"
	}
	out := make([][]*big.Int, len(paths))
	firstIn := make([]*big.Int, len(paths))
	for _, h := range hops {
		if h.Path < 0 || h.Path >= len(paths) {
			return nil, "mismatch"
		}
		j := len(out[h.Path])
		if j >= len(paths[h.Path].Hops) {
			return nil, "mismatch"
		}
		want := paths[h.Path].Hops[j]
		amt, ok := new(big.Int).SetString(h.ToAmount, 10)
		// Every decoded hop has a pool; lifiPaths' fallback path does not.
		if want.Pool == "" || lifiHopVenues[h.Venue] != want.Venue || label(h.FromToken.Address) != want.From ||
			label(h.ToToken.Address) != want.To || !ok || amt.Sign() <= 0 {
			return nil, "mismatch"
		}
		if j == 0 {
			in, ok := new(big.Int).SetString(h.FromAmount, 10)
			if !ok || in.Sign() <= 0 {
				return nil, "mismatch"
			}
			firstIn[h.Path] = in
		}
		out[h.Path] = append(out[h.Path], amt)
	}
	total := new(big.Int)
	for i := range paths {
		if len(out[i]) != len(paths[i].Hops) {
			return nil, "mismatch"
		}
		total.Add(total, firstIn[i])
	}
	for i := range paths {
		if shareString(firstIn[i], total) != paths[i].Share {
			return nil, "mismatch"
		}
	}
	return out, "attached"
}

// setHopAmounts formats raw onto the labelled paths, or sets none: a route
// with only some hop amounts would read as complete. A middle token that
// labelSACHops left as a contract ID (not a SAC, or its read failed) has no
// known decimals here.
func setHopAmounts(paths []types.SwapPath, raw [][]*big.Int, from, to Asset, fromDecimals, toDecimals int) string {
	formatted := make([][]string, len(paths))
	for i, p := range paths {
		for j, h := range p.Hops {
			var dec int
			switch {
			case h.To == to.ID:
				dec = toDecimals
			case h.To == from.ID:
				dec = fromDecimals
			case strkey.IsValidContractAddress(h.To):
				return "no_decimals"
			default:
				dec = ClassicDecimals
			}
			formatted[i] = append(formatted[i], FormatUnits(raw[i][j], dec))
		}
	}
	for i := range paths {
		for j := range paths[i].Hops {
			paths[i].Hops[j].ToAmount = formatted[i][j]
		}
	}
	return "attached"
}
