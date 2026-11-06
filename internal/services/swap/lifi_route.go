package swap

import (
	"encoding/hex"
	"errors"
	"math/big"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

var errNotLifiCall = errors.New("not a LI.FI swap call")

// lifiCall is the decoded argument tree of LI.FI's swap invocation.
// Args is the 7-element aggregator args vec; Args[5] is the distribution.
type lifiCall struct {
	Swap         xdr.InvokeContractArgs
	Args         []xdr.ScVal
	Fees         []xdr.ScVal
	MinAmountOut *big.Int
	Receiver     string
}

func decodeLifiCall(env xdr.TransactionEnvelope) (*lifiCall, error) {
	if env.Type != xdr.EnvelopeTypeEnvelopeTypeTx || env.V1 == nil {
		return nil, errNotLifiCall
	}
	ops := env.V1.Tx.Operations
	if len(ops) != 1 || ops[0].Body.InvokeHostFunctionOp == nil {
		return nil, errNotLifiCall
	}
	ic := ops[0].Body.InvokeHostFunctionOp.HostFunction.InvokeContract
	if ic == nil || len(ic.Args) != 2 {
		return nil, errNotLifiCall
	}
	params := ic.Args[0]
	argsVal, ok := scField(params, "args")
	args := scVec(argsVal)
	if !ok || len(args) != 7 {
		return nil, errNotLifiCall
	}
	feesVal, ok := scField(params, "fees")
	if !ok {
		return nil, errNotLifiCall
	}
	if _, isVec := feesVal.GetVec(); !isVec {
		return nil, errNotLifiCall
	}
	minVal, ok := scField(params, "min_amount_out")
	if !ok {
		return nil, errNotLifiCall
	}
	minOut, ok := scI128(minVal)
	if !ok {
		return nil, errNotLifiCall
	}
	receiver := scAddr(ic.Args[1])
	if receiver == "" {
		return nil, errNotLifiCall
	}
	return &lifiCall{Swap: *ic, Args: args, Fees: scVec(feesVal), MinAmountOut: minOut, Receiver: receiver}, nil
}

var lifiVenues = map[string]string{"Aqua": "Aquarius", "Sushi": "SushiSwap", "Soroswap": "Soroswap"}

// lifiPaths turns the call's distribution into display paths. Any entry it
// cannot read collapses the whole result to one direct Soroswap path, so the
// route is still shown; the unreadable venue is counted.
func lifiPaths(call *lifiCall, from, to Asset, fromContract, toContract string, m *metrics.Swap) []types.SwapPath {
	fallback := func(venue string) []types.SwapPath {
		metrics.RecordSwapUnknownHop(m, venue)
		return []types.SwapPath{{Share: "1", Hops: []types.SwapHop{{Venue: "Soroswap", From: from.ID, To: to.ID}}}}
	}
	if call == nil || len(call.Args) != 7 {
		return fallback("")
	}
	label := func(c string) string {
		switch c {
		case fromContract:
			return from.ID
		case toContract:
			return to.ID
		}
		return c
	}

	entries := scVec(call.Args[5])
	amounts := make([]*big.Int, len(entries))
	paths := make([]types.SwapPath, len(entries))
	total := new(big.Int)
	for i, e := range entries {
		amtVal, _ := scField(e, "amount_in")
		amt, ok := scI128(amtVal)
		if !ok || amt.Sign() <= 0 {
			return fallback("")
		}
		hopsVal, _ := scField(e, "hops")
		hopVals := scVec(hopsVal)
		if len(hopVals) == 0 {
			return fallback("")
		}
		for _, hv := range hopVals {
			hop := scVec(hv)
			if len(hop) != 2 {
				return fallback("")
			}
			venue := scSym(hop[0])
			hops, ok := readHop(venue, hop[1])
			if !ok {
				return fallback(venue)
			}
			for _, h := range hops {
				h.From, h.To = label(h.From), label(h.To)
				paths[i].Hops = append(paths[i].Hops, h)
			}
		}
		amounts[i] = amt
		total.Add(total, amt)
	}
	if len(entries) == 0 {
		return fallback("")
	}
	for i := range paths {
		paths[i].Share = shareString(amounts[i], total)
	}
	return paths
}

// readHop reads one venue hop; From/To are contract IDs.
func readHop(venue string, body xdr.ScVal) ([]types.SwapHop, bool) {
	name, known := lifiVenues[venue]
	if !known {
		return nil, false
	}
	field := func(k string) string {
		v, _ := scField(body, k)
		return scAddr(v)
	}
	switch venue {
	case "Soroswap", "Sushi":
		poolKey := "pair"
		if venue == "Sushi" {
			poolKey = "pool"
		}
		h := types.SwapHop{Venue: name, From: field("token_in"), To: field("token_out"), Pool: field(poolKey)}
		if h.From == "" || h.To == "" || h.Pool == "" {
			return nil, false
		}
		return []types.SwapHop{h}, true
	case "Aqua":
		prev := field("token_in")
		stepsVal, _ := scField(body, "steps")
		steps := scVec(stepsVal)
		if prev == "" || len(steps) == 0 {
			return nil, false
		}
		out := make([]types.SwapHop, 0, len(steps))
		for _, sv := range steps {
			step := scVec(sv)
			if len(step) != 3 {
				return nil, false
			}
			hash, ok := step[1].GetBytes()
			next := scAddr(step[2])
			if !ok || len(hash) != 32 || next == "" {
				return nil, false
			}
			out = append(out, types.SwapHop{Venue: name, From: prev, To: next, Pool: hex.EncodeToString(hash[:4]) + "…"})
			prev = next
		}
		return out, true
	}
	return nil, false
}

// shareString formats n/d with up to 4 decimals, trailing zeros trimmed.
func shareString(n, d *big.Int) string {
	s := new(big.Rat).SetFrac(n, d).FloatString(4)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
