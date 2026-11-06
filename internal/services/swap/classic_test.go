package swap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func horizonFixture(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for prefix, file := range routes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				b, err := os.ReadFile("testdata/" + file)
				require.NoError(t, err)
				_, _ = w.Write(b)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClassicQuoteSend(t *testing.T) {
	t.Parallel()
	srv := horizonFixture(t, map[string]string{"/paths/strict-send": "horizon_strict_send_xlm_usdc.json"})
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: srv.URL}}, nil)

	rt, err := src.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	assert.Equal(t, "classic", rt.Source)
	assert.Equal(t, "100", rt.FromAmount)
	assert.NotEmpty(t, rt.ToAmount)
	require.Len(t, rt.Paths, 1)
	assert.Equal(t, "1", rt.Paths[0].Share)
	hops := rt.Paths[0].Hops
	assert.Equal(t, "XLM", hops[0].From)
	assert.Equal(t, "USDC:"+usdcIssuer, hops[len(hops)-1].To)
	for _, h := range hops {
		assert.Equal(t, "stellar-dex", h.Venue)
		assert.Empty(t, h.Pool)
	}
	assert.Empty(t, rt.Fees)
}

// The default client keeps a pool wide enough for a burst: Go's default
// transport keeps two idle connections per host, so a second burst of ten
// re-dials eight (TCP+TLS inside the 4 s quote budget on a public Horizon).
func TestClassicQuoteReusesConnectionsAcrossBursts(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile("testdata/horizon_strict_send_xlm_usdc.json")
	require.NoError(t, err)
	var newConns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond) // hold the connection so the burst is concurrent
		_, _ = w.Write(fixture)
	}))
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			newConns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: srv.URL}}, nil)

	const burst = 10
	quoteBurst := func() {
		var wg sync.WaitGroup
		for i := 0; i < burst; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := src.Quote(context.Background(), sendReq)
				assert.NoError(t, err)
			}()
		}
		wg.Wait()
	}
	// A late goroutine may reuse a connection the burst already released, so
	// bound the dials rather than count them.
	quoteBurst()
	quoteBurst()
	assert.LessOrEqual(t, newConns.Load(), int32(burst), "the second burst reuses the first burst's connections")
}

func TestClassicQuoteReceive(t *testing.T) {
	t.Parallel()
	srv := horizonFixture(t, map[string]string{"/paths/strict-receive": "horizon_strict_receive_usdc_xlm.json"})
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: srv.URL}}, nil)
	req := types.SwapQuoteRequest{
		Network: types.PUBLIC, Mode: types.SwapModeReceive,
		FromAsset: "USDC:" + usdcIssuer, ToAsset: "XLM", Amount: "0.5",
	}
	rt, err := src.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "0.5", rt.ToAmount)
	assert.NotEmpty(t, rt.FromAmount)
}

func TestClassicQuoteNoRoute(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"_embedded":{"records":[]}}`))
	}))
	t.Cleanup(srv.Close)
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: srv.URL}}, nil)
	_, err := src.Quote(context.Background(), sendReq)
	assert.ErrorIs(t, err, ErrNoRoute)
}

func TestClassicSupportsConfiguredNetworksAndClassicPairs(t *testing.T) {
	t.Parallel()
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "x", types.TESTNET: "y"}}, nil)
	assert.True(t, src.Supports(sendReq))
	req := sendReq
	req.Network = types.FUTURENET
	assert.False(t, src.Supports(req))
	req = sendReq
	req.ToAsset = "CBI7UCH5KGSVQRO5H4SUCZUTZABCITZLRHQQZTWL2TK4RZ72TAR6IHRV"
	assert.False(t, src.Supports(req))
	_, err := src.Quote(context.Background(), req)
	assert.ErrorIs(t, err, ErrBadRequest)
}

const aquaIssuer = "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA"

// fakeLedger serves real XDR entries keyed by the base64 keys BuildTx requests.
type fakeLedger struct {
	utils.MockRPCService
	account string
	entries map[string]string
}

func newFakeLedger(t *testing.T, account string, seq int64, hasTrustline bool) *fakeLedger {
	t.Helper()
	f := &fakeLedger{account: account, entries: map[string]string{}}
	if account == "" {
		return f
	}
	id, err := xdr.AddressToAccountId(account)
	require.NoError(t, err)

	var ak xdr.LedgerKey
	require.NoError(t, ak.SetAccount(id))
	akB64, err := xdr.MarshalBase64(ak)
	require.NoError(t, err)
	acctData := xdr.LedgerEntryData{
		Type:    xdr.LedgerEntryTypeAccount,
		Account: &xdr.AccountEntry{AccountId: id, SeqNum: xdr.SequenceNumber(seq)},
	}
	f.entries[akB64], err = xdr.MarshalBase64(acctData)
	require.NoError(t, err)

	if hasTrustline {
		usdc := xdr.MustNewCreditAsset("USDC", usdcIssuer)
		var tk xdr.LedgerKey
		require.NoError(t, tk.SetTrustline(id, usdc.ToTrustLineAsset()))
		tkB64, err := xdr.MarshalBase64(tk)
		require.NoError(t, err)
		tlData := xdr.LedgerEntryData{
			Type:      xdr.LedgerEntryTypeTrustline,
			TrustLine: &xdr.TrustLineEntry{AccountId: id, Asset: usdc.ToTrustLineAsset(), Balance: 0, Limit: 1 << 62},
		}
		f.entries[tkB64], err = xdr.MarshalBase64(tlData)
		require.NoError(t, err)
	}
	return f
}

func (f *fakeLedger) GetLedgerEntriesXDR(_ context.Context, keys []string, _ string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := f.entries[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func TestClassicBuild(t *testing.T) {
	t.Parallel()
	for _, hasTrustline := range []bool{true, false} {
		acct := keypair.MustRandom().Address()
		rpc := newFakeLedger(t, acct, 41, hasTrustline)
		src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "unused"}}, rpc)
		xdrB64, err := src.BuildTx(context.Background(), types.SwapBuildRequest{
			Network: types.PUBLIC, Account: acct, Source: "classic",
			FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0002", Timeout: 180,
			Paths: []types.SwapPath{{Share: "1", Hops: []types.SwapHop{
				{Venue: "stellar-dex", From: "XLM", To: "AQUA:" + aquaIssuer},
				{Venue: "stellar-dex", From: "AQUA:" + aquaIssuer, To: "USDC:" + usdcIssuer},
			}}},
		})
		require.NoError(t, err)
		adds, err := AddsTrustline(xdrB64, acct, "")
		require.NoError(t, err)
		assert.Equal(t, !hasTrustline, adds)

		var env xdr.TransactionEnvelope
		require.NoError(t, xdr.SafeUnmarshalBase64(xdrB64, &env))
		assert.Equal(t, int64(42), env.SeqNum())
		ops := env.Operations()
		wantOps := 1
		if !hasTrustline {
			wantOps = 2
		}
		require.Len(t, ops, wantOps)
		assert.Equal(t, int64(2000), int64(env.Fee()))
		assert.Equal(t, int64(2000/wantOps*wantOps), int64(env.Fee()))

		last := ops[len(ops)-1].Body
		require.Equal(t, xdr.OperationTypePathPaymentStrictSend, last.Type)
		pp := last.PathPaymentStrictSendOp
		assert.Equal(t, xdr.Int64(175000000), pp.DestMin)
		assert.Equal(t, xdr.Int64(1000000000), pp.SendAmount)
		assert.Equal(t, acct, pp.Destination.Address())
		assert.Equal(t, xdr.AssetTypeAssetTypeNative, pp.SendAsset.Type)
		assert.Equal(t, "USDC", pp.DestAsset.GetCode())
		require.Len(t, pp.Path, 1)
		assert.Equal(t, "AQUA", pp.Path[0].GetCode())

		tb := env.Preconditions().TimeBounds
		require.NotNil(t, tb)
		assert.LessOrEqual(t, int64(tb.MaxTime), time.Now().Unix()+300)
		assert.Greater(t, int64(tb.MaxTime), time.Now().Unix())
	}
}

// The build must follow the quoted route: one path from fromAsset to toAsset
// with at most five intermediates (the PathPaymentStrictSendOp limit, which
// txnbuild does not enforce, so a longer path encodes but cannot decode).
func TestClassicBuildRejectsPathsNotMatchingTheQuote(t *testing.T) {
	t.Parallel()
	xlmToUSDC := []types.SwapHop{{From: "XLM", To: "USDC:" + usdcIssuer}}
	var long []types.SwapHop
	prev := "XLM"
	for i := 0; i < 6; i++ {
		mid := fmt.Sprintf("A%d:%s", i, keypair.MustRandom().Address())
		long = append(long, types.SwapHop{From: prev, To: mid})
		prev = mid
	}
	long = append(long, types.SwapHop{From: prev, To: "USDC:" + usdcIssuer})

	for name, paths := range map[string][]types.SwapPath{
		"no path":           nil,
		"two paths":         {{Share: "0.5", Hops: xlmToUSDC}, {Share: "0.5", Hops: xlmToUSDC}},
		"no hops":           {{Share: "1"}},
		"starts elsewhere":  {{Share: "1", Hops: []types.SwapHop{{From: "USDC:" + usdcIssuer, To: "AQUA:" + aquaIssuer}, {From: "AQUA:" + aquaIssuer, To: "USDC:" + usdcIssuer}}}},
		"ends elsewhere":    {{Share: "1", Hops: []types.SwapHop{{From: "XLM", To: "AQUA:" + aquaIssuer}, {From: "AQUA:" + aquaIssuer, To: "XLM"}}}},
		"six intermediates": {{Share: "1", Hops: long}},
	} {
		t.Run(name, func(t *testing.T) {
			acct := keypair.MustRandom().Address()
			src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "unused"}}, newFakeLedger(t, acct, 41, true))
			x, err := src.BuildTx(context.Background(), types.SwapBuildRequest{
				Network: types.PUBLIC, Account: acct, Source: "classic",
				FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0002", Timeout: 180,
				Paths: paths,
			})
			assert.ErrorIs(t, err, ErrBadRequest)
			assert.Empty(t, x)
		})
	}
}

func TestClassicBuildIssuerNeedsNoTrustline(t *testing.T) {
	t.Parallel()
	rpc := newFakeLedger(t, usdcIssuer, 41, false)
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "unused"}}, rpc)
	xdrB64, err := src.BuildTx(context.Background(), types.SwapBuildRequest{
		Network: types.PUBLIC, Account: usdcIssuer, Source: "classic",
		FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0001", Timeout: 180,
		Paths: []types.SwapPath{{Share: "1", Hops: []types.SwapHop{{Venue: "stellar-dex", From: "XLM", To: "USDC:" + usdcIssuer}}}},
	})
	require.NoError(t, err)
	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(xdrB64, &env))
	require.Len(t, env.Operations(), 1)
	assert.Equal(t, xdr.OperationTypePathPaymentStrictSend, env.Operations()[0].Body.Type)
}

func TestClassicBuildTimeout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		timeout int64
		want    int64
	}{{180, 180}, {60, 60}} {
		acct := keypair.MustRandom().Address()
		src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "unused"}}, newFakeLedger(t, acct, 41, true))
		before := time.Now().Unix()
		xdrB64, err := src.BuildTx(context.Background(), types.SwapBuildRequest{
			Network: types.PUBLIC, Account: acct, Source: "classic",
			FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0001",
			Timeout: tc.timeout,
			Paths: []types.SwapPath{{Share: "1", Hops: []types.SwapHop{
				{Venue: "stellar-dex", From: "XLM", To: "USDC:" + usdcIssuer},
			}}},
		})
		require.NoError(t, err)
		var env xdr.TransactionEnvelope
		require.NoError(t, xdr.SafeUnmarshalBase64(xdrB64, &env))
		maxTime := int64(env.Preconditions().TimeBounds.MaxTime)
		assert.GreaterOrEqual(t, maxTime, before+tc.want)
		assert.LessOrEqual(t, maxTime, time.Now().Unix()+tc.want)
	}
}

// A zero timeout would build a transaction that never expires.
func TestClassicBuildRejectsZeroTimeout(t *testing.T) {
	t.Parallel()
	acct := keypair.MustRandom().Address()
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "unused"}}, newFakeLedger(t, acct, 41, true))
	_, err := src.BuildTx(context.Background(), types.SwapBuildRequest{
		Network: types.PUBLIC, Account: acct, Source: "classic",
		FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0001",
		Timeout: 0,
		Paths: []types.SwapPath{{Share: "1", Hops: []types.SwapHop{
			{Venue: "stellar-dex", From: "XLM", To: "USDC:" + usdcIssuer},
		}}},
	})
	require.ErrorIs(t, err, ErrBadRequest)
}

func TestClassicBuildAccountNotFound(t *testing.T) {
	t.Parallel()
	rpc := newFakeLedger(t, "", 0, false)
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "unused"}}, rpc)
	_, err := src.BuildTx(context.Background(), types.SwapBuildRequest{
		Network: types.PUBLIC,
		Account: keypair.MustRandom().Address(), FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer,
		Amount: "1", MinOut: "0.1", Fee: "0.00001", Timeout: 180,
	})
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// A body past maxUpstreamBody is cut off: a decode error, not an empty page
// read as no_route.
func TestClassicOversizedBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"_embedded":{"records":[]},"pad":"`))
		_, _ = w.Write(bytes.Repeat([]byte("a"), 3<<20))
		_, _ = w.Write([]byte(`"}`))
	}))
	defer srv.Close()
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: srv.URL}}, nil)
	_, err := src.Quote(context.Background(), sendReq)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoRoute)
}

func TestClassicBuildFeeFloor(t *testing.T) {
	t.Parallel()
	for _, hasTrustline := range []bool{true, false} {
		acct := keypair.MustRandom().Address()
		src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "unused"}}, newFakeLedger(t, acct, 41, hasTrustline))
		xdrB64, err := src.BuildTx(context.Background(), types.SwapBuildRequest{
			Network: types.PUBLIC, Account: acct, Source: "classic",
			FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0000001", Timeout: 180,
			Paths: []types.SwapPath{{Share: "1", Hops: []types.SwapHop{{Venue: "stellar-dex", From: "XLM", To: "USDC:" + usdcIssuer}}}},
		})
		require.NoError(t, err)
		var env xdr.TransactionEnvelope
		require.NoError(t, xdr.SafeUnmarshalBase64(xdrB64, &env))
		assert.Equal(t, int64(100*len(env.Operations())), int64(env.Fee()))
	}
}

// orderbookFixture serves POST /v1/quote with the given status and body.
func orderbookFixture(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/quote" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// countingHorizon answers strict-send from the fixture and counts calls.
func countingHorizon(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	fixture := horizonFixture(t, map[string]string{"/paths/strict-send": "horizon_strict_send_xlm_usdc.json"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, fixture.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newOrderbookSource(horizonURL, orderbookURL string, m *metrics.Swap) *ClassicSource {
	return NewClassicSource(ClassicConfig{
		HorizonURLs:   map[string]string{types.PUBLIC: horizonURL},
		OrderbookURLs: map[string]string{types.PUBLIC: orderbookURL},
		Metrics:       m,
	}, nil)
}

const eurcIssuer = "GDHU6WRG4IEQXM5NZ4BMPKOXHW76MZM4Y2IEMFDVXBSDP6SJY4ITNPP2"

func TestClassicQuoteFromOrderbook(t *testing.T) {
	t.Parallel()
	hz, hzCalls := countingHorizon(t)
	ob := orderbookFixture(t, http.StatusOK, `{"mode":"send","fromAsset":"XLM","toAsset":"USDC:`+usdcIssuer+`",
		"fromAmount":"100.0000000","toAmount":"17.5450814","path":["EURC:`+eurcIssuer+`"],"ledger":59123456}`)
	src := newOrderbookSource(hz.URL, ob.URL, nil)

	rt, err := src.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	assert.Equal(t, int32(0), hzCalls.Load())
	assert.Equal(t, "classic", rt.Source)
	assert.Equal(t, "100", rt.FromAmount)
	assert.Equal(t, "17.5450814", rt.ToAmount)
	require.Len(t, rt.Paths, 1)
	assert.Equal(t, []types.SwapHop{
		{Venue: "stellar-dex", From: "XLM", To: "EURC:" + eurcIssuer},
		{Venue: "stellar-dex", From: "EURC:" + eurcIssuer, To: "USDC:" + usdcIssuer},
	}, rt.Paths[0].Hops)
}

func TestClassicQuoteFromOrderbookReceive(t *testing.T) {
	t.Parallel()
	var got map[string]string
	ob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"mode":"receive","fromAsset":"USDC:` + usdcIssuer + `","toAsset":"XLM","fromAmount":"2.8500000","toAmount":"0.5000000","path":[],"ledger":1}`))
	}))
	t.Cleanup(ob.Close)
	src := newOrderbookSource("http://unused.invalid", ob.URL, nil)
	req := types.SwapQuoteRequest{
		Network: types.PUBLIC, Mode: types.SwapModeReceive,
		FromAsset: "USDC:" + usdcIssuer, ToAsset: "XLM", Amount: "0.5",
	}

	rt, err := src.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"mode": "receive", "fromAsset": "USDC:" + usdcIssuer, "toAsset": "XLM", "amount": "0.5"}, got)
	assert.Equal(t, "2.8500000", rt.FromAmount)
	assert.Equal(t, "0.5", rt.ToAmount)
	assert.Equal(t, []types.SwapHop{{Venue: "stellar-dex", From: "USDC:" + usdcIssuer, To: "XLM"}}, rt.Paths[0].Hops)
}

// A no_route answer is final: Horizon runs the same search.
func TestClassicQuoteOrderbookNoRoute(t *testing.T) {
	t.Parallel()
	hz, hzCalls := countingHorizon(t)
	ob := orderbookFixture(t, http.StatusNotFound, `{"code":"no_route","message":"no path"}`)
	m := metrics.NewSwap(prometheus.NewRegistry())
	src := newOrderbookSource(hz.URL, ob.URL, m)

	_, err := src.Quote(context.Background(), sendReq)
	assert.ErrorIs(t, err, ErrNoRoute)
	assert.Equal(t, int32(0), hzCalls.Load())
	assert.Equal(t, 0, testutil.CollectAndCount(m.OrderbookFallbacks))
}

// sendEcho opens a 200 body that repeats sendReq's mode and pair.
var sendEcho = `{"mode":"send","fromAsset":"XLM","toAsset":"USDC:` + usdcIssuer + `",`

// longPath is six intermediates, one more than a classic build accepts.
var longPath = strings.Repeat("USDC:"+usdcIssuer+`","`, 5) + "USDC:" + usdcIssuer

func TestClassicQuoteFallsBackToHorizon(t *testing.T) {
	t.Parallel()
	hang := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // the server sees the client hang up only after the body is read
		<-r.Context().Done()
	}))
	t.Cleanup(hang.Close)
	cases := []struct {
		name, url, reason string
	}{
		{"not ready", orderbookFixture(t, http.StatusServiceUnavailable, `{"code":"not_ready","message":"loading"}`).URL, "not_ready"},
		{"search timeout", orderbookFixture(t, http.StatusGatewayTimeout, `{"code":"timeout","message":"slow"}`).URL, "timeout"},
		{"hung", hang.URL, "timeout"},
		{"unreachable", "http://127.0.0.1:1", "error"}, // a closed test server's port can be reused by a parallel test
		{"bad request", orderbookFixture(t, http.StatusBadRequest, `{"code":"invalid_request","message":"x"}`).URL, "error"},
		{"bad path asset", orderbookFixture(t, http.StatusOK, sendEcho+`"fromAmount":"100","toAmount":"1","path":["native"]}`).URL, "error"},
		{"bad json", orderbookFixture(t, http.StatusOK, `{`).URL, "error"},
		{"404 not no_route", orderbookFixture(t, http.StatusNotFound, `{"code":"not_found","message":"x"}`).URL, "error"},
		{"404 plain text", orderbookFixture(t, http.StatusNotFound, `404 page not found`).URL, "error"},
		{"missing amount", orderbookFixture(t, http.StatusOK, sendEcho+`"fromAmount":"100","path":[]}`).URL, "error"},
		{"zero amount", orderbookFixture(t, http.StatusOK, sendEcho+`"fromAmount":"100","toAmount":"0.0000000","path":[]}`).URL, "error"},
		{"fraction amount", orderbookFixture(t, http.StatusOK, sendEcho+`"fromAmount":"100","toAmount":"1/3","path":[]}`).URL, "error"},
		{"path longer than a build allows", orderbookFixture(t, http.StatusOK, sendEcho+`"fromAmount":"100","toAmount":"1","path":["`+longPath+`"]}`).URL, "error"},
		{"other pair", orderbookFixture(t, http.StatusOK, `{"mode":"send","fromAsset":"XLM","toAsset":"AQUA:`+aquaIssuer+`","fromAmount":"100","toAmount":"1","path":[]}`).URL, "error"},
		{"other mode", orderbookFixture(t, http.StatusOK, `{"mode":"receive","fromAsset":"XLM","toAsset":"USDC:`+usdcIssuer+`","fromAmount":"100","toAmount":"1","path":[]}`).URL, "error"},
		{"fixed side not the request", orderbookFixture(t, http.StatusOK, sendEcho+`"fromAmount":"60","toAmount":"10","path":[]}`).URL, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hz, hzCalls := countingHorizon(t)
			m := metrics.NewSwap(prometheus.NewRegistry())
			src := newOrderbookSource(hz.URL, tc.url, m)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			rt, err := src.Quote(ctx, sendReq)
			require.NoError(t, err)
			assert.Equal(t, int32(1), hzCalls.Load())
			assert.NotEmpty(t, rt.ToAmount)
			assert.Equal(t, 1.0, testutil.ToFloat64(m.OrderbookFallbacks.WithLabelValues(types.PUBLIC, tc.reason)))
		})
	}
}

// A network with no orderbook URL goes straight to Horizon.
func TestClassicQuoteOrderbookOtherNetwork(t *testing.T) {
	t.Parallel()
	hz, hzCalls := countingHorizon(t)
	src := NewClassicSource(ClassicConfig{
		HorizonURLs:   map[string]string{types.TESTNET: hz.URL},
		OrderbookURLs: map[string]string{types.PUBLIC: "http://unused.invalid"},
	}, nil)
	req := sendReq
	req.Network = types.TESTNET

	_, err := src.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int32(1), hzCalls.Load())
}

// Each network's sdex-orderbook is asked for that network, and its fallbacks
// are counted under it.
func TestClassicQuoteOrderbookPerNetwork(t *testing.T) {
	t.Parallel()
	hz, hzCalls := countingHorizon(t)
	m := metrics.NewSwap(prometheus.NewRegistry())
	src := NewClassicSource(ClassicConfig{
		HorizonURLs: map[string]string{types.TESTNET: hz.URL},
		OrderbookURLs: map[string]string{
			types.PUBLIC:  "http://unused.invalid",
			types.TESTNET: orderbookFixture(t, http.StatusServiceUnavailable, `{"code":"not_ready","message":"loading"}`).URL,
		},
		Metrics: m,
	}, nil)
	req := sendReq
	req.Network = types.TESTNET

	_, err := src.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int32(1), hzCalls.Load())
	assert.Equal(t, 1.0, testutil.ToFloat64(m.OrderbookFallbacks.WithLabelValues(types.TESTNET, "not_ready")))
}

// hangingOrderbook accepts the quote and never answers; arrived is closed
// once the request is in.
func hangingOrderbook(t *testing.T) (*httptest.Server, chan struct{}) {
	t.Helper()
	arrived := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // the server sees the client hang up only after the body is read
		once.Do(func() { close(arrived) })
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv, arrived
}

// Without a caller deadline, a hung sdex-orderbook still gives up quickly.
func TestClassicQuoteOrderbookHungNoDeadline(t *testing.T) {
	t.Parallel()
	hz, hzCalls := countingHorizon(t)
	ob, _ := hangingOrderbook(t)
	m := metrics.NewSwap(prometheus.NewRegistry())
	src := newOrderbookSource(hz.URL, ob.URL, m)

	start := time.Now()
	_, err := src.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 5*time.Second) // not the client timeout
	assert.Equal(t, int32(1), hzCalls.Load())
	assert.Equal(t, 1.0, testutil.ToFloat64(m.OrderbookFallbacks.WithLabelValues(types.PUBLIC, "timeout")))
}

// A caller that goes away is not a fallback: Horizon is not asked.
func TestClassicQuoteOrderbookCallerGone(t *testing.T) {
	t.Parallel()
	hz, hzCalls := countingHorizon(t)
	ob, arrived := hangingOrderbook(t)
	m := metrics.NewSwap(prometheus.NewRegistry())
	src := newOrderbookSource(hz.URL, ob.URL, m)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-arrived; cancel() }()

	_, err := src.Quote(ctx, sendReq)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(0), hzCalls.Load())
	assert.Equal(t, 0, testutil.CollectAndCount(m.OrderbookFallbacks))
}

// With a 1s per-source budget, a hung sdex-orderbook still leaves Horizon time.
func TestClassicQuoteOrderbookHungShortBudget(t *testing.T) {
	t.Parallel()
	hz, hzCalls := countingHorizon(t)
	ob, _ := hangingOrderbook(t)
	m := metrics.NewSwap(prometheus.NewRegistry())
	src := newOrderbookSource(hz.URL, ob.URL, m)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := src.Quote(ctx, sendReq)
	require.NoError(t, err)
	assert.Equal(t, int32(1), hzCalls.Load())
	assert.Equal(t, 1.0, testutil.ToFloat64(m.OrderbookFallbacks.WithLabelValues(types.PUBLIC, "timeout")))
}

// A caller that leaves while the body is still arriving is not a fallback.
func TestClassicQuoteOrderbookCallerGoneMidBody(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			hz, hzCalls := countingHorizon(t)
			headers := make(chan struct{})
			ob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"code":`))
				w.(http.Flusher).Flush()
				close(headers)
				<-r.Context().Done()
			}))
			t.Cleanup(ob.Close)
			m := metrics.NewSwap(prometheus.NewRegistry())
			src := newOrderbookSource(hz.URL, ob.URL, m)
			ctx, cancel := context.WithCancel(context.Background())
			go func() { <-headers; time.Sleep(200 * time.Millisecond); cancel() }() // after Do returns, during the body read

			_, err := src.Quote(ctx, sendReq)
			assert.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, int32(0), hzCalls.Load())
			assert.Equal(t, 0, testutil.CollectAndCount(m.OrderbookFallbacks))
		})
	}
}

func TestClassicQuoteHorizon429IsRateLimited(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: srv.URL}}, nil)
	_, err := src.Quote(context.Background(), sendReq)
	assert.Equal(t, "rate_limited", Reason(err))
}
