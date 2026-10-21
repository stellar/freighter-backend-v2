package swap

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

func lifiServer(t *testing.T, status int, body []byte) *LifiSource {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return NewLifiSource(LifiConfig{Client: NewLifiClient(srv.URL, "", srv.Client())}, nil)
}

func lifiReq(amount string) types.SwapQuoteRequest {
	return types.SwapQuoteRequest{Network: types.PUBLIC, Account: "GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN7",
		Mode: types.SwapModeSend, FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: amount}
}

func fmtRaw(t *testing.T, s string, decimals int) string {
	t.Helper()
	v, ok := new(big.Int).SetString(s, 10)
	require.True(t, ok, s)
	return FormatUnits(v, decimals)
}

func TestLifiQuoteTrustFixture(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	var q LifiQuote
	require.NoError(t, json.Unmarshal(body, &q))
	src := lifiServer(t, 200, body)

	req := lifiReq(fmtRaw(t, q.Action.FromAmount, 7))
	rt, err := src.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "lifi", rt.Source)
	assert.Equal(t, req.Amount, rt.FromAmount)
	assert.Equal(t, fmtRaw(t, q.Estimate.ToAmount, 7), rt.ToAmount)
	require.Len(t, q.Estimate.FeeCosts, 2)
	assert.Equal(t, []types.SwapFee{
		{Name: "LI.FI fee", Amount: fmtRaw(t, q.Estimate.FeeCosts[0].Amount, 7), Asset: "XLM"},
		{Name: "Soroswap fee", Amount: fmtRaw(t, q.Estimate.FeeCosts[1].Amount, 7), Asset: "XLM"},
	}, rt.Fees)
	require.NotEmpty(t, rt.Paths)
	assert.Equal(t, "XLM", rt.Paths[0].Hops[0].From)
}

func TestLifiQuoteShrinkIsInsufficientBalance(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_shrink.json")
	var q LifiQuote
	require.NoError(t, json.Unmarshal(body, &q))
	req := lifiReq("100")
	require.NotEqual(t, "1000000000", q.Action.FromAmount)
	_, err := lifiServer(t, 200, body).Quote(context.Background(), req)
	assert.ErrorIs(t, err, ErrInsufficientBalance)
}

func TestLifiQuote1003IsNoRoute(t *testing.T) {
	_, err := lifiServer(t, 404, lifiFixture(t, "lifi_err_1003.json")).Quote(context.Background(), lifiReq("10"))
	assert.ErrorIs(t, err, ErrNoRoute)
}

func TestLifiQuoteReceiveUnsupported(t *testing.T) {
	req := lifiReq("10")
	req.Mode = types.SwapModeReceive
	_, err := lifiServer(t, 200, []byte(`{}`)).Quote(context.Background(), req)
	assert.ErrorIs(t, err, ErrUnsupportedMode)
}

func TestLifiQuoteTooManyDecimalsIsBadRequest(t *testing.T) {
	_, err := lifiServer(t, 200, []byte(`{}`)).Quote(context.Background(), lifiReq("1.12345678"))
	assert.ErrorIs(t, err, ErrBadRequest)
}

func TestLifiQuoteContractTokenDecimals(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	var q LifiQuote
	require.NoError(t, json.Unmarshal(body, &q))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/token" {
			_, _ = w.Write(lifiFixture(t, "lifi_token_dejtrsy.json"))
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	src := NewLifiSource(LifiConfig{Client: NewLifiClient(srv.URL, "", srv.Client())}, nil)
	req := lifiReq(fmtRaw(t, q.Action.FromAmount, 7))
	req.ToAsset = "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75"
	rt, err := src.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, fmtRaw(t, q.Estimate.ToAmount, 18), rt.ToAmount)
}

// Review Focus 4: a 200 without a usable transaction is reason "error", and
// the Router still returns the classic route.
func TestLifiQuoteMissingTransaction(t *testing.T) {
	bodies := map[string]string{
		"no transactionRequest": `{"action":{"fromAmount":"100000000"},"estimate":{"toAmount":"18433149","feeCosts":[]}}`,
		"undecodable xdr":       `{"action":{"fromAmount":"100000000"},"estimate":{"toAmount":"18433149","feeCosts":[]},"transactionRequest":{"data":"not-xdr"}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			src := lifiServer(t, 200, []byte(body))
			var err error
			require.NotPanics(t, func() { _, err = src.Quote(context.Background(), lifiReq("10")) })
			require.ErrorIs(t, err, ErrLifiMissingTx)
			assert.Equal(t, "error", Reason(err))

			r := NewRouter([]Source{src, &fakeSource{name: "classic", route: route("classic", "10", "1.8")}}, time.Second, time.Second, nil)
			res, err := r.Quote(context.Background(), lifiReq("10"))
			require.NoError(t, err)
			require.Len(t, res.Routes, 1)
			assert.Equal(t, "classic", res.Routes[0].Source)
			assert.Equal(t, []types.SwapUnavailable{{Source: "lifi", Reason: "error"}}, res.Unavailable)
		})
	}
}

func TestLifiSupportsPublicOnly(t *testing.T) {
	src := NewLifiSource(LifiConfig{}, nil)
	assert.Equal(t, "lifi", src.Name())
	assert.True(t, src.Supports(types.PUBLIC))
	assert.False(t, src.Supports(types.TESTNET))
}

func lifiBuildSource(t *testing.T, env xdr.TransactionEnvelope, status int, body []byte) *LifiSource {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	maxTime := int64(env.V1.Tx.Cond.TimeBounds.MaxTime)
	return NewLifiSource(LifiConfig{
		Client:         NewLifiClient(srv.URL, "", srv.Client()),
		SwapContract:   lifiSwapContract,
		FeeRecipient:   lifiFeeRecipient,
		Aggregator:     lifiAggregator,
		MaxResourceFee: 3000000,
		Now:            func() time.Time { return time.Unix(maxTime-300, 0) },
	}, nil)
}

func lifiBuildReq(t *testing.T, env xdr.TransactionEnvelope) types.SwapBuildRequest {
	t.Helper()
	call, err := decodeLifiCall(env)
	require.NoError(t, err)
	return types.SwapBuildRequest{Network: types.PUBLIC, Account: lifiTestAccount, Source: "lifi",
		FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "10",
		MinOut: FormatUnits(call.MinAmountOut, 7), Fee: "0.0001"}
}

func TestLifiBuildTrustFixture(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	env := decodeEnv(t, trustFixtureXDR(t))
	src := lifiBuildSource(t, env, 200, body)

	x, err := src.BuildTx(context.Background(), lifiBuildReq(t, env))
	require.NoError(t, err)
	out := decodeEnv(t, x)
	assert.Equal(t, uint32(out.V1.Tx.Ext.SorobanData.ResourceFee)+1000, uint32(out.V1.Tx.Fee))
	assert.Empty(t, out.V1.Signatures)
	adds, err := AddsTrustline(x, lifiTestAccount, contractIDOf(t, "USDC:"+usdcIssuer))
	require.NoError(t, err)
	assert.True(t, adds)
}

func TestLifiBuildInclusionFeeFloor(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	req := lifiBuildReq(t, env)
	req.Fee = "0.0000001"
	x, err := lifiBuildSource(t, env, 200, lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")).BuildTx(context.Background(), req)
	require.NoError(t, err)
	out := decodeEnv(t, x)
	assert.Equal(t, uint32(out.V1.Tx.Ext.SorobanData.ResourceFee)+100, uint32(out.V1.Tx.Fee))
}

func TestLifiBuildPriceMovedIsQuoteMoved(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	req := lifiBuildReq(t, env)
	call, err := decodeLifiCall(env)
	require.NoError(t, err)
	req.MinOut = FormatUnits(new(big.Int).Add(call.MinAmountOut, big.NewInt(1)), 7)
	_, err = lifiBuildSource(t, env, 200, lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")).BuildTx(context.Background(), req)
	assert.ErrorIs(t, err, ErrQuoteMoved)
}

func TestLifiBuildNoRouteIsQuoteMoved(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	_, err := lifiBuildSource(t, env, 404, []byte(`{"code":1002,"message":"no quote"}`)).BuildTx(context.Background(), lifiBuildReq(t, env))
	assert.ErrorIs(t, err, ErrQuoteMoved)
}

func TestLifiBuildShrinkIsQuoteMoved(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	req := lifiBuildReq(t, env)
	req.Amount = "11"
	_, err := lifiBuildSource(t, env, 200, lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")).BuildTx(context.Background(), req)
	assert.ErrorIs(t, err, ErrQuoteMoved)
}

// A tampered transaction from LI.FI is refused, not handed to the user.
func TestLifiBuildTamperedIsCheckFailed(t *testing.T) {
	var q map[string]any
	require.NoError(t, json.Unmarshal(lifiFixture(t, "lifi_quote_xlm_usdc_trust.json"), &q))
	env := decodeEnv(t, trustFixtureXDR(t))
	tampered := decodeEnv(t, trustFixtureXDR(t))
	tampered.V1.Tx.Ext.SorobanData.ResourceFee = 3000001
	b64, err := xdr.MarshalBase64(tampered)
	require.NoError(t, err)
	q["transactionRequest"].(map[string]any)["data"] = b64
	body, err := json.Marshal(q)
	require.NoError(t, err)
	_, err = lifiBuildSource(t, env, 200, body).BuildTx(context.Background(), lifiBuildReq(t, env))
	assert.ErrorIs(t, err, ErrCheckFailed)
}

func TestLifiBuildBadRequest(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	src := lifiBuildSource(t, env, 200, lifiFixture(t, "lifi_quote_xlm_usdc_trust.json"))
	for name, mut := range map[string]func(r *types.SwapBuildRequest){
		"from asset": func(r *types.SwapBuildRequest) { r.FromAsset = "nope" },
		"to asset":   func(r *types.SwapBuildRequest) { r.ToAsset = "nope" },
		"amount":     func(r *types.SwapBuildRequest) { r.Amount = "x" },
		"min out":    func(r *types.SwapBuildRequest) { r.MinOut = "x" },
		"fee":        func(r *types.SwapBuildRequest) { r.Fee = "x" },
	} {
		t.Run(name, func(t *testing.T) {
			req := lifiBuildReq(t, env)
			mut(&req)
			_, err := src.BuildTx(context.Background(), req)
			assert.ErrorIs(t, err, ErrBadRequest)
		})
	}
}

func TestLifiQuoteFromAmountComparedNumerically(t *testing.T) {
	var q map[string]any
	require.NoError(t, json.Unmarshal(lifiFixture(t, "lifi_quote_xlm_usdc_trust.json"), &q))
	for fromAmount, wantErr := range map[string]bool{"0100000000": false, "1e8": true, "": true} {
		t.Run(fromAmount, func(t *testing.T) {
			q["action"].(map[string]any)["fromAmount"] = fromAmount
			body, err := json.Marshal(q)
			require.NoError(t, err)
			_, err = lifiServer(t, 200, body).Quote(context.Background(), lifiReq("10"))
			if wantErr {
				require.Error(t, err)
				assert.NotErrorIs(t, err, ErrInsufficientBalance)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// LI.FI's signatures cannot authorize anything for the user; they are dropped.
func TestLifiBuildClearsSignatures(t *testing.T) {
	var q map[string]any
	require.NoError(t, json.Unmarshal(lifiFixture(t, "lifi_quote_xlm_usdc_trust.json"), &q))
	env := decodeEnv(t, trustFixtureXDR(t))
	signed := decodeEnv(t, trustFixtureXDR(t))
	signed.V1.Signatures = []xdr.DecoratedSignature{{Hint: xdr.SignatureHint{1, 2, 3, 4}, Signature: xdr.Signature{5}}}
	b64, err := xdr.MarshalBase64(signed)
	require.NoError(t, err)
	q["transactionRequest"].(map[string]any)["data"] = b64
	body, err := json.Marshal(q)
	require.NoError(t, err)
	x, err := lifiBuildSource(t, env, 200, body).BuildTx(context.Background(), lifiBuildReq(t, env))
	require.NoError(t, err)
	assert.Empty(t, decodeEnv(t, x).V1.Signatures)
}

func TestLifiBuildMissingTransaction(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	for name, body := range map[string]string{
		"no transactionRequest": `{"action":{"fromAmount":"100000000"}}`,
		"undecodable xdr":       `{"action":{"fromAmount":"100000000"},"transactionRequest":{"data":"not-xdr"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := lifiBuildSource(t, env, 200, []byte(body)).BuildTx(context.Background(), lifiBuildReq(t, env))
			require.ErrorIs(t, err, ErrLifiMissingTx)
			assert.Equal(t, "error", Reason(err))
		})
	}
}

// Out-of-range token decimals make the source error rather than panic in
// ParseUnits or allocate a huge string.
func TestLifiBadTokenDecimals(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	quote := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	for _, d := range []int{40, -1} {
		for _, side := range []string{"from", "to"} {
			t.Run(fmt.Sprintf("%s %d", side, d), func(t *testing.T) {
				var tokenCalls int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/token" {
						atomic.AddInt32(&tokenCalls, 1)
						_, _ = fmt.Fprintf(w, `{"decimals":%d}`, d)
						return
					}
					_, _ = w.Write(quote)
				}))
				defer srv.Close()
				src := NewLifiSource(LifiConfig{Client: NewLifiClient(srv.URL, "", srv.Client())}, nil)
				qreq := lifiReq("10")
				breq := lifiBuildReq(t, env)
				if side == "from" {
					qreq.FromAsset, breq.FromAsset = otherContract, otherContract
				} else {
					qreq.ToAsset, breq.ToAsset = otherContract, otherContract
				}
				var qerr, berr error
				require.NotPanics(t, func() {
					_, qerr = src.Quote(context.Background(), qreq)
					_, berr = src.BuildTx(context.Background(), breq)
				})
				require.Error(t, qerr)
				require.Error(t, berr)
				assert.Equal(t, "error", Reason(qerr))
				assert.Equal(t, "error", Reason(berr))
				var ue *metrics.UpstreamError
				assert.ErrorAs(t, qerr, &ue)
				assert.ErrorAs(t, berr, &ue)
				assert.EqualValues(t, 2, atomic.LoadInt32(&tokenCalls), "bad decimals must not be cached")
			})
		}
	}
}

func slippageRecorder(t *testing.T, env xdr.TransactionEnvelope, body []byte) (*LifiSource, *[]string) {
	t.Helper()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Query().Get("slippage"))
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	maxTime := int64(env.V1.Tx.Cond.TimeBounds.MaxTime)
	return NewLifiSource(LifiConfig{
		Client:         NewLifiClient(srv.URL, "", srv.Client()),
		SwapContract:   lifiSwapContract,
		FeeRecipient:   lifiFeeRecipient,
		Aggregator:     lifiAggregator,
		MaxResourceFee: 3000000,
		Now:            func() time.Time { return time.Unix(maxTime-300, 0) },
	}, nil), &got
}

func TestLifiSlippageSentToClient(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	env := decodeEnv(t, trustFixtureXDR(t))

	src, got := slippageRecorder(t, env, body)
	req := lifiBuildReq(t, env)
	req.Slippage = "2"
	_, err := src.BuildTx(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, []string{"0.02"}, *got)

	src, got = slippageRecorder(t, env, body)
	_, err = src.BuildTx(context.Background(), lifiBuildReq(t, env))
	require.NoError(t, err)
	assert.Equal(t, []string{"0.005"}, *got)

	var q LifiQuote
	require.NoError(t, json.Unmarshal(body, &q))
	src, got = slippageRecorder(t, env, body)
	_, err = src.Quote(context.Background(), lifiReq(fmtRaw(t, q.Action.FromAmount, 7)))
	require.NoError(t, err)
	assert.Equal(t, []string{"0.005"}, *got)
}

func TestSlippageFraction(t *testing.T) {
	for in, want := range map[string]string{"1": "0.01", "2": "0.02", "9": "0.09", "10": "0.1", "05": "0.05"} {
		got, err := SlippageFraction(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "0", "00", "0.5", "1.25", "2.0", "11", "-1", "abc", "1e1", " 1", "+1", "100"} {
		_, err := SlippageFraction(in)
		assert.Error(t, err, in)
	}
}
