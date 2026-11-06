package swap

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// sourceContract drives one Source against fake upstreams that can answer
// every request in it. See the Source doc comment for the rules it checks.
type sourceContract struct {
	src Source
	// quotes are requests the fake upstream answers with a route. One the
	// source does not Support is skipped.
	quotes []types.SwapQuoteRequest
	// build is a request the fake upstream answers with a transaction.
	build types.SwapBuildRequest
	// now is the source's clock, for the expiry check.
	now func() time.Time
}

// checkSourceContract runs the rules every Source must follow. Call it from a
// new source's tests with a sourceContract wired to that source's fakes.
func checkSourceContract(t *testing.T, c sourceContract) {
	t.Helper()
	assert.NotEmpty(t, c.src.Name())
	unknown := c.quotes[0]
	unknown.Network = "NOT_A_NETWORK"
	assert.False(t, c.src.Supports(unknown), "Supports must be false for an unknown network")

	// A fake that never answers fails the call instead of hanging the package.
	bounded := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 10*time.Second)
	}

	var answered []types.SwapQuoteRequest
	for _, req := range c.quotes {
		t.Run("quote "+req.Mode, func(t *testing.T) {
			if !c.src.Supports(req) {
				t.Skipf("%s does not offer %s", c.src.Name(), req.Mode)
			}
			ctx, cancel := bounded()
			defer cancel()
			rt, err := c.src.Quote(ctx, req)
			require.NoError(t, err)
			checkRoute(t, c.src.Name(), req, rt)
			answered = append(answered, req)
		})
	}
	require.NotEmpty(t, answered, "at least one quote must return a route")

	t.Run("quote with a done context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := c.src.Quote(ctx, answered[0])
		assert.ErrorIs(t, err, context.Canceled, "Quote must not answer once ctx is done")
	})

	t.Run("build", func(t *testing.T) {
		ctx, cancel := bounded()
		defer cancel()
		x, err := c.src.BuildTx(ctx, c.build)
		require.NoError(t, err)
		var env xdr.TransactionEnvelope
		require.NoError(t, xdr.SafeUnmarshalBase64(x, &env))
		assert.Equal(t, c.build.Account, env.SourceAccount().ToAccountId().Address())
		assert.Empty(t, env.Signatures(), "the transaction must be unsigned")
		cond := env.Preconditions()
		tb := cond.TimeBounds
		if cond.V2 != nil {
			tb = cond.V2.TimeBounds
		}
		require.NotNil(t, tb, "the transaction must expire")
		assert.InDelta(t, c.now().Unix()+c.build.Timeout, int64(tb.MaxTime), 5, "expiry must be now + Timeout")
	})

	for _, timeout := range []int64{0, MaxTimeoutSeconds + 1} {
		req := c.build
		req.Timeout = timeout
		ctx, cancel := bounded()
		_, err := c.src.BuildTx(ctx, req)
		cancel()
		assert.ErrorIs(t, err, ErrBadRequest, "timeout %d", timeout)
	}
}

func checkRoute(t *testing.T, name string, req types.SwapQuoteRequest, rt *types.SwapRoute) {
	t.Helper()
	require.NotNil(t, rt)
	assert.Equal(t, name, rt.Source)
	fixed, other := rt.FromAmount, rt.ToAmount
	if req.Mode == types.SwapModeReceive {
		fixed, other = rt.ToAmount, rt.FromAmount
	}
	assert.Zero(t, rat(req.Amount).Cmp(rat(fixed)), "the fixed side %q must equal the request amount %q", fixed, req.Amount)
	assert.Positive(t, rat(other).Sign(), "the quoted side %q must be a positive decimal", other)
	assert.NotNil(t, rt.Fees, "Fees must be [] rather than null when there are none")

	require.NotEmpty(t, rt.Paths)
	total := new(big.Rat)
	for _, p := range rt.Paths {
		assert.Positive(t, rat(p.Share).Sign(), "share %q", p.Share)
		total.Add(total, rat(p.Share))
		require.NotEmpty(t, p.Hops)
		assert.Equal(t, req.FromAsset, p.Hops[0].From, "a path starts at the asset sold")
		assert.Equal(t, req.ToAsset, p.Hops[len(p.Hops)-1].To, "a path ends at the asset bought")
		for i := 0; i+1 < len(p.Hops); i++ {
			assert.Equal(t, p.Hops[i].To, p.Hops[i+1].From, "hops %d and %d must connect", i, i+1)
		}
	}
	// Shares may be rounded for display.
	f, _ := total.Float64()
	assert.InDelta(t, 1, f, 0.001, "path shares must sum to 1")
}

func TestClassicSourceContract(t *testing.T) {
	t.Parallel()
	srv := horizonFixture(t, map[string]string{
		"/paths/strict-send":    "horizon_strict_send_xlm_usdc.json",
		"/paths/strict-receive": "horizon_strict_receive_usdc_xlm.json",
	})
	acct := keypair.MustRandom().Address()
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: srv.URL}}, newFakeLedger(t, acct, 41, true))

	checkSourceContract(t, sourceContract{
		src: src,
		quotes: []types.SwapQuoteRequest{
			sendReq,
			{Network: types.PUBLIC, Mode: types.SwapModeReceive, FromAsset: "USDC:" + usdcIssuer, ToAsset: "XLM", Amount: "0.5"},
		},
		build: types.SwapBuildRequest{
			Network: types.PUBLIC, Account: acct, Source: classicName,
			FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0002", Timeout: 180,
			Paths: []types.SwapPath{{Share: "1", Hops: []types.SwapHop{{Venue: "stellar-dex", From: "XLM", To: "USDC:" + usdcIssuer}}}},
		},
		now: time.Now,
	})
}

func TestLifiSourceContract(t *testing.T) {
	t.Parallel()
	body := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	var q LifiQuote
	require.NoError(t, json.Unmarshal(body, &q))
	env := decodeEnv(t, trustFixtureXDR(t))
	src := lifiBuildSource(t, env, 200, body)
	send := lifiReq(fmtRaw(t, q.Action.FromAmount, 7))
	receive := send
	receive.Mode = types.SwapModeReceive

	checkSourceContract(t, sourceContract{
		src:    src,
		quotes: []types.SwapQuoteRequest{send, receive},
		build:  lifiBuildReq(t, env),
		now:    func() time.Time { return time.Unix(int64(env.V1.Tx.Cond.TimeBounds.MaxTime)-300, 0) },
	})
}

// Classic quotes from sdex-orderbook follow the same contract as from Horizon.
func TestClassicOrderbookSourceContract(t *testing.T) {
	t.Parallel()
	ob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Mode, FromAsset, ToAsset, Amount string }
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		from, to := req.Amount, "17.5450814"
		if req.Mode == types.SwapModeReceive {
			from, to = "2.8500000", req.Amount
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mode": req.Mode, "fromAsset": req.FromAsset, "toAsset": req.ToAsset,
			"fromAmount": from, "toAmount": to, "path": []string{"EURC:" + eurcIssuer}, "ledger": 1,
		})
	}))
	t.Cleanup(ob.Close)
	acct := keypair.MustRandom().Address()
	src := newOrderbookSource("http://unused.invalid", ob.URL, nil)
	src.rpc = newFakeLedger(t, acct, 41, true)

	checkSourceContract(t, sourceContract{
		src: src,
		quotes: []types.SwapQuoteRequest{
			sendReq,
			{Network: types.PUBLIC, Mode: types.SwapModeReceive, FromAsset: "USDC:" + usdcIssuer, ToAsset: "XLM", Amount: "0.5"},
		},
		build: types.SwapBuildRequest{
			Network: types.PUBLIC, Account: acct, Source: classicName,
			FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0002", Timeout: 180,
			Paths: []types.SwapPath{{Share: "1", Hops: []types.SwapHop{{Venue: "stellar-dex", From: "XLM", To: "USDC:" + usdcIssuer}}}},
		},
		now: time.Now,
	})
}
