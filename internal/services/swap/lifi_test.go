package swap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

// USDC (Circle) mainnet SAC, the contract in the LI.FI fixtures.
const usdcContract = "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75"

func lifiServer(t *testing.T, status int, body []byte) *LifiSource {
	t.Helper()
	return lifiServerRPC(t, status, body, &fakeDecimalsRPC{fn: decimalsOf(7)})
}

func lifiServerRPC(t *testing.T, status int, body []byte, rpc types.RPCService) *LifiSource {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return NewLifiSource(LifiConfig{Client: NewLifiClient(srv.URL, "", srv.Client()), RPC: rpc}, nil)
}

// fakeDecimalsRPC answers decimals() simulations through fn and records them.
type fakeDecimalsRPC struct {
	utils.MockRPCService
	fn func(contract string) (*xdr.ScVal, error)

	mu       sync.Mutex
	calls    map[string]int
	last     simulated
	inflight int
	peak     int
}

type simulated struct {
	function string
	account  string
	network  string
}

func (f *fakeDecimalsRPC) SimulateInvocation(_ context.Context, id xdr.ScAddress, acct *txnbuild.SimpleAccount, fn xdr.ScSymbol,
	_ []xdr.ScVal, _ txnbuild.TimeBounds, network string) (types.SimulateTransactionResponse, error) {
	contract := addrString(id)
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[contract]++
	f.last = simulated{function: string(fn), account: acct.AccountID, network: network}
	f.inflight++
	f.peak = max(f.peak, f.inflight)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()
	return f.fn(contract)
}

func (f *fakeDecimalsRPC) callsTo(contract string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[contract]
}

func (f *fakeDecimalsRPC) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

func decimalsOf(n uint32) func(string) (*xdr.ScVal, error) {
	return func(string) (*xdr.ScVal, error) {
		v := u32Val(n)
		return &v, nil
	}
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
			t.Errorf("decimals must not come from LI.FI")
			_, _ = w.Write([]byte(`{"decimals":18}`))
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	rpc := &fakeDecimalsRPC{fn: decimalsOf(7)}
	src := NewLifiSource(LifiConfig{Client: NewLifiClient(srv.URL, "", srv.Client()), RPC: rpc}, nil)
	req := lifiReq(fmtRaw(t, q.Action.FromAmount, 7))
	req.ToAsset = usdcContract
	rt, err := src.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, fmtRaw(t, q.Estimate.ToAmount, 7), rt.ToAmount)
	assert.Equal(t, simulated{function: "decimals", account: req.Account, network: types.PUBLIC}, rpc.last)
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
	return lifiBuildSourceRPC(t, env, status, body, &fakeDecimalsRPC{fn: decimalsOf(7)})
}

func lifiBuildSourceRPC(t *testing.T, env xdr.TransactionEnvelope, status int, body []byte, rpc types.RPCService) *LifiSource {
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
		PartnerID:      lifiPartnerID,
		Aggregator:     lifiAggregator,
		MaxResourceFee: 3000000,
		RPC:            rpc,
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

func TestLifiBuildTimeout(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	env := decodeEnv(t, trustFixtureXDR(t))
	now := int64(env.V1.Tx.Cond.TimeBounds.MaxTime) - 300
	long := int64(3600)
	for _, tc := range []struct {
		timeout *int64
		want    int64
	}{{nil, now + 180}, {&long, now + 3600}} {
		req := lifiBuildReq(t, env)
		req.Timeout = tc.timeout
		x, err := lifiBuildSource(t, env, 200, body).BuildTx(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, xdr.TimePoint(tc.want), decodeEnv(t, x).V1.Tx.Cond.TimeBounds.MaxTime)
	}
}

// A MinTime past the user's expiry would make an unsubmittable tx.
func TestLifiBuildMinTimeAfterTimeoutIsCheckFailed(t *testing.T) {
	var q map[string]any
	require.NoError(t, json.Unmarshal(lifiFixture(t, "lifi_quote_xlm_usdc_trust.json"), &q))
	env := decodeEnv(t, trustFixtureXDR(t))
	late := decodeEnv(t, trustFixtureXDR(t))
	late.V1.Tx.Cond.TimeBounds.MinTime = late.V1.Tx.Cond.TimeBounds.MaxTime - 200 // now+100
	b64, err := xdr.MarshalBase64(late)
	require.NoError(t, err)
	q["transactionRequest"].(map[string]any)["data"] = b64
	body, err := json.Marshal(q)
	require.NoError(t, err)
	req := lifiBuildReq(t, env)
	one := int64(1)
	req.Timeout = &one
	_, err = lifiBuildSource(t, env, 200, body).BuildTx(context.Background(), req)
	require.ErrorIs(t, err, ErrCheckFailed)
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

// A bad decimals() result makes the source error rather than panic in
// ParseUnits or allocate a huge string, and is never cached.
func TestLifiBadTokenDecimals(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	quote := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	str := xdr.ScString("7")
	i32 := xdr.Int32(7)
	for name, val := range map[string]*xdr.ScVal{
		"u32 40":  ptr(u32Val(40)),
		"u32 19":  ptr(u32Val(19)),
		"i32":     {Type: xdr.ScValTypeScvI32, I32: &i32},
		"string":  {Type: xdr.ScValTypeScvString, Str: &str},
		"void":    {Type: xdr.ScValTypeScvVoid},
		"nil":     nil,
		"u32 nil": {Type: xdr.ScValTypeScvU32},
	} {
		for _, side := range []string{"from", "to"} {
			t.Run(fmt.Sprintf("%s %s", side, name), func(t *testing.T) {
				rpc := &fakeDecimalsRPC{fn: func(string) (*xdr.ScVal, error) { return val, nil }}
				src := lifiServerRPC(t, 200, quote, rpc)
				bsrc := lifiBuildSourceRPC(t, env, 200, quote, rpc)
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
					_, berr = bsrc.BuildTx(context.Background(), breq)
				})
				require.Error(t, qerr)
				require.Error(t, berr)
				assert.Equal(t, "error", Reason(qerr))
				assert.Equal(t, "error", Reason(berr))
				var ue *metrics.UpstreamError
				require.ErrorAs(t, qerr, &ue)
				assert.Equal(t, "invalid_response", ue.Kind)
				require.ErrorAs(t, berr, &ue)
				assert.Equal(t, "invalid_response", ue.Kind)
				assert.Equal(t, 2, rpc.callsTo(otherContract), "bad decimals must not be cached (quote and build each read)")
			})
		}
	}
}

func ptr[T any](v T) *T { return &v }

// Build decides minOut with the chain's decimals: a value LI.FI would accept
// (7) is refused when the chain says 8, and a build is accepted at the chain's
// value even though LI.FI's own token metadata would disagree.
func TestLifiBuildUsesChainDecimals(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_usdc_100k.json")
	var q LifiQuote
	require.NoError(t, json.Unmarshal(body, &q))
	env := decodeEnv(t, q.TransactionRequest.Data)
	req := lifiBuildReq(t, env)
	req.Account = "GDHF3HIKWM5KJAVLZBSZWUFDOEOT7IBMY22UXG4QBE326O354INLPAND"
	req.ToAsset = usdcContract
	req.Amount = "100000"
	min := mustLifiCall(t, env).MinAmountOut

	ok := &fakeDecimalsRPC{fn: decimalsOf(7)}
	req.MinOut = FormatUnits(min, 7)
	_, err := lifiBuildSourceRPC(t, env, 200, body, ok).BuildTx(context.Background(), req)
	require.NoError(t, err)

	moved := &fakeDecimalsRPC{fn: decimalsOf(8)}
	_, err = lifiBuildSourceRPC(t, env, 200, body, moved).BuildTx(context.Background(), req)
	assert.ErrorIs(t, err, ErrQuoteMoved)
}

func TestLifiDecimalsCachedPerContract(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	var q LifiQuote
	require.NoError(t, json.Unmarshal(body, &q))

	rpc := &fakeDecimalsRPC{fn: decimalsOf(7)}
	src := lifiServerRPC(t, 200, body, rpc)
	req := lifiReq(fmtRaw(t, q.Action.FromAmount, 7))
	req.ToAsset = usdcContract
	for range 2 {
		_, err := src.Quote(context.Background(), req)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, rpc.callsTo(usdcContract))
	assert.Equal(t, 1, rpc.total())

	req.FromAsset = otherContract
	_, _ = src.Quote(context.Background(), req)
	_, _ = src.Quote(context.Background(), req)
	assert.Equal(t, 1, rpc.callsTo(otherContract))
	assert.Equal(t, 1, rpc.callsTo(usdcContract))

	classic := &fakeDecimalsRPC{fn: decimalsOf(7)}
	csrc := lifiServerRPC(t, 200, body, classic)
	_, err := csrc.Quote(context.Background(), lifiReq(fmtRaw(t, q.Action.FromAmount, 7)))
	require.NoError(t, err)
	assert.Zero(t, classic.total())
}

func TestLifiDecimalsErrorNotCached(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	var q LifiQuote
	require.NoError(t, json.Unmarshal(body, &q))
	env := decodeEnv(t, trustFixtureXDR(t))

	var n atomic.Int32
	rpc := &fakeDecimalsRPC{fn: func(string) (*xdr.ScVal, error) {
		if n.Add(1) == 1 {
			return nil, errors.New("rpc down")
		}
		v := u32Val(7)
		return &v, nil
	}}
	src := lifiServerRPC(t, 200, body, rpc)
	req := lifiReq(fmtRaw(t, q.Action.FromAmount, 7))
	req.ToAsset = usdcContract

	_, err := src.Quote(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, "error", Reason(err))
	assert.ErrorAs(t, err, new(*metrics.UpstreamError), "mapSwapError answers 502 for an UpstreamError")

	_, err = src.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 2, rpc.callsTo(usdcContract))

	brpc := &fakeDecimalsRPC{fn: func(string) (*xdr.ScVal, error) { return nil, errors.New("rpc down") }}
	breq := lifiBuildReq(t, env)
	breq.ToAsset = usdcContract
	_, err = lifiBuildSourceRPC(t, env, 200, body, brpc).BuildTx(context.Background(), breq)
	assert.ErrorAs(t, err, new(*metrics.UpstreamError))
	assert.Equal(t, "error", Reason(err))

	erpc := &fakeDecimalsRPC{fn: func(string) (*xdr.ScVal, error) { return nil, errors.New("rpc down") }}
	res, err := NewRouter([]Source{lifiServerRPC(t, 200, body, erpc), &fakeSource{name: "classic", route: route("classic", "10", "1.8")}},
		time.Second, time.Second, nil).Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, []types.SwapUnavailable{{Source: "lifi", Reason: "error"}}, res.Unavailable)
}

// Both tokens are read together, so a cold quote costs one round trip.
func TestLifiDecimalsReadConcurrently(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	var arrived atomic.Int32
	ready := make(chan struct{})
	rpc := &fakeDecimalsRPC{fn: func(string) (*xdr.ScVal, error) {
		if arrived.Add(1) == 2 {
			close(ready)
		}
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			return nil, errors.New("second read never started")
		}
		v := u32Val(7)
		return &v, nil
	}}
	req := lifiReq("10")
	req.FromAsset, req.ToAsset = otherContract, usdcContract
	_, _ = lifiServerRPC(t, 200, body, rpc).Quote(context.Background(), req)
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	assert.Equal(t, 2, rpc.peak)
	assert.Equal(t, 2, len(rpc.calls))
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
		PartnerID:      lifiPartnerID,
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
	for in, want := range map[string]string{"0": "0", "0.5": "0.005", "1.5": "0.015", "2": "0.02", "10": "0.1", "2.0": "0.02", "1.25": "0.0125", "10.0": "0.1", "05": "0.05"} {
		got, err := SlippageFraction(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "10.01", "11", "-1", "abc", "1,5", ".5", "1.", "1e1", "1/2", " 1", "+1", "100", "1.2.3", "0x1", "1234567890123456789012"} {
		_, err := SlippageFraction(in)
		assert.Error(t, err, in)
	}
}

// A 200 whose fromAmount is unusable is an upstream fault (502), not a 500.
func TestLifiMalformedFromAmountIsUpstreamError(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	for name, body := range map[string]string{
		"missing":     `{"action":{},"estimate":{"toAmount":"1"}}`,
		"non-numeric": `{"action":{"fromAmount":"1e8"},"estimate":{"toAmount":"1"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := lifiServer(t, 200, []byte(body)).Quote(context.Background(), lifiReq("10"))
			assert.ErrorAs(t, err, new(*metrics.UpstreamError))

			_, err = lifiBuildSource(t, env, 200, []byte(body)).BuildTx(context.Background(), lifiBuildReq(t, env))
			assert.ErrorAs(t, err, new(*metrics.UpstreamError))
		})
	}
}

func TestLifiBuildFeeOverflowIsBadRequest(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	req := lifiBuildReq(t, env)
	req.Fee = "430"
	_, err := lifiBuildSource(t, env, 200, lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")).BuildTx(context.Background(), req)
	assert.ErrorIs(t, err, ErrBadRequest)
}

// The trust fixture opens a trustline on the destination, which only a classic
// asset may have; the same contract as a non-SAC token must be refused.
func TestLifiBuildTrustOnNonClassicDestinationIsCheckFailed(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	req := lifiBuildReq(t, env)
	req.ToAsset = contractIDOf(t, "USDC:"+usdcIssuer)
	req.MinOut = FormatUnits(new(big.Int).Set(mustLifiCall(t, env).MinAmountOut), 0)
	_, err := lifiBuildSource(t, env, 200, lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")).BuildTx(context.Background(), req)
	require.ErrorIs(t, err, ErrCheckFailed)
	assert.Contains(t, err.Error(), "auth trust")
}

func mustLifiCall(t *testing.T, env xdr.TransactionEnvelope) *lifiCall {
	t.Helper()
	call, err := decodeLifiCall(env)
	require.NoError(t, err)
	return call
}
