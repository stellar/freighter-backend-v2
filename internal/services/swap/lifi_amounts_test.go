package swap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// lifi_quote_xlm_eurc_hops.json is a live /v1/quote: path 0 is XLM→EURC on
// Aquarius, path 1 is XLM→USDC on Aquarius then USDC→EURC on Soroswap.
const hopsFixture = "lifi_quote_xlm_eurc_hops.json"

// hopsQuote quotes XLM→EURC from body, with USDC labelled when labelUSDC.
func hopsQuote(t *testing.T, body []byte, labelUSDC bool) (*types.SwapRoute, *metrics.Swap) {
	t.Helper()
	rt, m, err := hopsQuoteErr(t, body, labelUSDC)
	require.NoError(t, err)
	return rt, m
}

func hopsQuoteErr(t *testing.T, body []byte, labelUSDC bool) (*types.SwapRoute, *metrics.Swap, error) {
	t.Helper()
	var q LifiQuote
	require.NoError(t, json.Unmarshal(body, &q))
	rpc := &fakeDecimalsRPC{fn: decimalsOf(7)}
	if labelUSDC {
		rpc.ledger = instanceEntries(t, map[string]string{usdcContract: "USDC:" + usdcIssuer})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	m := metrics.NewSwap(prometheus.NewRegistry())
	src := NewLifiSource(LifiConfig{Client: NewLifiClient(srv.URL, "", srv.Client()), RPC: rpc}, m)
	req := lifiReq(fmtRaw(t, q.Action.FromAmount, 7))
	req.ToAsset = eurcContract
	rt, err := src.Quote(context.Background(), req)
	return rt, m, err
}

// editHops rewrites the fixture's estimate.hops through edit.
func editHops(t *testing.T, edit func(hops []any) []any) []byte {
	t.Helper()
	var raw map[string]any
	require.NoError(t, json.Unmarshal(lifiFixture(t, hopsFixture), &raw))
	for _, s := range raw["includedSteps"].([]any) {
		est := s.(map[string]any)["estimate"].(map[string]any)
		if hops, ok := est["hops"].([]any); ok {
			est["hops"] = edit(hops)
		}
	}
	body, err := json.Marshal(raw)
	require.NoError(t, err)
	return body
}

func hopAmounts(paths []types.SwapPath) [][]string {
	out := make([][]string, len(paths))
	for i, p := range paths {
		for _, h := range p.Hops {
			out[i] = append(out[i], h.ToAmount)
		}
	}
	return out
}

func TestLifiHopAmountsAttached(t *testing.T) {
	rt, m := hopsQuote(t, lifiFixture(t, hopsFixture), true)
	assert.Equal(t, [][]string{{"39.7618746"}, {"130.1363633", "118.9939446"}}, hopAmounts(rt.Paths))
	assert.Equal(t, "USDC:"+usdcIssuer, rt.Paths[1].Hops[0].To)
	assert.Equal(t, 1.0, testutil.ToFloat64(m.HopAmounts.WithLabelValues("attached")))
}

func TestLifiHopAmountsWithheld(t *testing.T) {
	cases := map[string]struct {
		body      func(t *testing.T) []byte
		labelUSDC bool
		result    string
	}{
		"no estimate.hops": {
			body:      func(t *testing.T) []byte { return editHops(t, func([]any) []any { return nil }) },
			labelUSDC: true, result: "missing",
		},
		"venue differs from the transaction": {
			body: func(t *testing.T) []byte {
				return editHops(t, func(h []any) []any { h[2].(map[string]any)["venue"] = "Comet"; return h })
			},
			labelUSDC: true, result: "mismatch",
		},
		"token differs from the transaction": {
			body: func(t *testing.T) []byte {
				return editHops(t, func(h []any) []any {
					h[0].(map[string]any)["toToken"].(map[string]any)["address"] = usdcContract
					return h
				})
			},
			labelUSDC: true, result: "mismatch",
		},
		"a hop is missing": {
			body:      func(t *testing.T) []byte { return editHops(t, func(h []any) []any { return h[:2] }) },
			labelUSDC: true, result: "mismatch",
		},
		"an extra hop": {
			body:      func(t *testing.T) []byte { return editHops(t, func(h []any) []any { return append(h, h[0]) }) },
			labelUSDC: true, result: "mismatch",
		},
		"path index out of range": {
			body: func(t *testing.T) []byte {
				return editHops(t, func(h []any) []any { h[0].(map[string]any)["path"] = 2; return h })
			},
			labelUSDC: true, result: "mismatch",
		},
		"non-positive amount": {
			body: func(t *testing.T) []byte {
				return editHops(t, func(h []any) []any { h[1].(map[string]any)["toAmount"] = "0"; return h })
			},
			labelUSDC: true, result: "mismatch",
		},
		// The Aquarius pool is never compared, so only the decode error can withhold.
		"estimate in an unexpected shape": {
			body: func(t *testing.T) []byte {
				return editHops(t, func(h []any) []any { h[0].(map[string]any)["pool"] = 7; return h })
			},
			labelUSDC: true, result: "mismatch",
		},
		"pool differs from the transaction": {
			body: func(t *testing.T) []byte {
				return editHops(t, func(h []any) []any { h[2].(map[string]any)["pool"] = h[0].(map[string]any)["pool"]; return h })
			},
			labelUSDC: true, result: "mismatch",
		},
		"split differs from the transaction": {
			body: func(t *testing.T) []byte {
				return editHops(t, func(h []any) []any { h[0].(map[string]any)["fromAmount"] = "1"; return h })
			},
			labelUSDC: true, result: "mismatch",
		},
		"middle token not labelled": {
			body:      func(t *testing.T) []byte { return lifiFixture(t, hopsFixture) },
			labelUSDC: false, result: "no_decimals",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rt, m := hopsQuote(t, tc.body(t), tc.labelUSDC)
			for _, p := range rt.Paths {
				for _, h := range p.Hops {
					assert.Empty(t, h.ToAmount)
				}
			}
			assert.Equal(t, 1.0, testutil.ToFloat64(m.HopAmounts.WithLabelValues(tc.result)))
		})
	}
}

func TestLifiHopAmountsAttachedWithoutEstimatePool(t *testing.T) {
	body := editHops(t, func(h []any) []any { delete(h[2].(map[string]any), "pool"); return h })
	rt, m := hopsQuote(t, body, true)
	assert.Equal(t, [][]string{{"39.7618746"}, {"130.1363633", "118.9939446"}}, hopAmounts(rt.Paths))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.HopAmounts.WithLabelValues("attached")))
}

func TestLifiHopAmountsNotOnFallbackPath(t *testing.T) {
	// lifiPaths' stand-in when it cannot read the distribution: not decoded
	// from the transaction, so it has no pool.
	fallback := []types.SwapPath{{Share: "1", Hops: []types.SwapHop{{Venue: "Soroswap", From: "XLM", To: "EURC"}}}}
	var q LifiQuote
	require.NoError(t, json.Unmarshal([]byte(`{"includedSteps":[{"estimate":{"hops":[{"venue":"Soroswap","path":0,
		"fromToken":{"address":"XLM"},"toToken":{"address":"EURC"},"fromAmount":"10","toAmount":"9"}]}}]}`), &q))
	raw, result := lifiHopAmounts(&q, fallback, func(c string) string { return c })
	assert.Nil(t, raw)
	assert.Equal(t, "mismatch", result)
}

func TestLifiHopAmountsNotCountedForRefusedRoute(t *testing.T) {
	var raw map[string]any
	require.NoError(t, json.Unmarshal(lifiFixture(t, hopsFixture), &raw))
	txReq := raw["transactionRequest"].(map[string]any)
	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(txReq["data"].(string), &env))
	env.V1.Tx.Ext.SorobanData.ResourceFee = -1
	b64, err := xdr.MarshalBase64(env)
	require.NoError(t, err)
	txReq["data"] = b64
	body, err := json.Marshal(raw)
	require.NoError(t, err)

	_, m, err := hopsQuoteErr(t, body, true)
	require.Error(t, err)
	assert.Equal(t, 0, testutil.CollectAndCount(m.HopAmounts))
}

func TestLifiHopAmountsEmptyPathIsMismatch(t *testing.T) {
	paths := []types.SwapPath{
		{Share: "1", Hops: []types.SwapHop{{Venue: "Soroswap", From: "XLM", To: "EURC", Pool: "P"}}},
		{Share: "0"},
	}
	var q LifiQuote
	require.NoError(t, json.Unmarshal([]byte(`{"includedSteps":[{"estimate":{"hops":[{"venue":"Soroswap","path":0,
		"fromToken":{"address":"XLM"},"toToken":{"address":"EURC"},"fromAmount":"10","toAmount":"9"}]}}]}`), &q))
	raw, result := lifiHopAmounts(&q, paths, func(c string) string { return c })
	assert.Nil(t, raw)
	assert.Equal(t, "mismatch", result)
}
