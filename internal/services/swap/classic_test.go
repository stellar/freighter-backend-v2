package swap

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestClassicQuoteReceive(t *testing.T) {
	t.Parallel()
	srv := horizonFixture(t, map[string]string{"/paths/strict-receive": "horizon_strict_receive_usdc_xlm.json"})
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: srv.URL}}, nil)
	req := types.SwapQuoteRequest{Network: types.PUBLIC, Mode: types.SwapModeReceive,
		FromAsset: "USDC:" + usdcIssuer, ToAsset: "XLM", Amount: "0.5"}
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

	req := sendReq
	req.ToAsset = "CBI7UCH5KGSVQRO5H4SUCZUTZABCITZLRHQQZTWL2TK4RZ72TAR6IHRV"
	_, err = src.Quote(context.Background(), req)
	assert.ErrorIs(t, err, ErrNoRoute) // not called for Soroban-only assets
}

func TestClassicSupportsConfiguredNetworksOnly(t *testing.T) {
	t.Parallel()
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "x", types.TESTNET: "y"}}, nil)
	assert.True(t, src.Supports(types.TESTNET))
	assert.False(t, src.Supports(types.FUTURENET))
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
	acctData := xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeAccount,
		Account: &xdr.AccountEntry{AccountId: id, SeqNum: xdr.SequenceNumber(seq)}}
	f.entries[akB64], err = xdr.MarshalBase64(acctData)
	require.NoError(t, err)

	if hasTrustline {
		usdc := xdr.MustNewCreditAsset("USDC", usdcIssuer)
		var tk xdr.LedgerKey
		require.NoError(t, tk.SetTrustline(id, usdc.ToTrustLineAsset()))
		tkB64, err := xdr.MarshalBase64(tk)
		require.NoError(t, err)
		tlData := xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeTrustline,
			TrustLine: &xdr.TrustLineEntry{AccountId: id, Asset: usdc.ToTrustLineAsset(), Balance: 0, Limit: 1 << 62}}
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
			FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0002",
			Paths: []types.SwapPath{{Share: "1", Hops: []types.SwapHop{
				{Venue: "stellar-dex", From: "XLM", To: "AQUA:" + aquaIssuer},
				{Venue: "stellar-dex", From: "AQUA:" + aquaIssuer, To: "USDC:" + usdcIssuer}}}},
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

func TestClassicBuildIssuerNeedsNoTrustline(t *testing.T) {
	t.Parallel()
	rpc := newFakeLedger(t, usdcIssuer, 41, false)
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "unused"}}, rpc)
	xdrB64, err := src.BuildTx(context.Background(), types.SwapBuildRequest{
		Network: types.PUBLIC, Account: usdcIssuer, Source: "classic",
		FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0001",
	})
	require.NoError(t, err)
	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(xdrB64, &env))
	require.Len(t, env.Operations(), 1)
	assert.Equal(t, xdr.OperationTypePathPaymentStrictSend, env.Operations()[0].Body.Type)
}

func TestClassicBuildTimeout(t *testing.T) {
	t.Parallel()
	sixty := int64(60)
	for _, tc := range []struct {
		timeout *int64
		want    int64
	}{{nil, 180}, {&sixty, 60}} {
		acct := keypair.MustRandom().Address()
		src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "unused"}}, newFakeLedger(t, acct, 41, true))
		before := time.Now().Unix()
		xdrB64, err := src.BuildTx(context.Background(), types.SwapBuildRequest{
			Network: types.PUBLIC, Account: acct, Source: "classic",
			FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0001",
			Timeout: tc.timeout,
			Paths: []types.SwapPath{{Share: "1", Hops: []types.SwapHop{
				{Venue: "stellar-dex", From: "XLM", To: "USDC:" + usdcIssuer}}}},
		})
		require.NoError(t, err)
		var env xdr.TransactionEnvelope
		require.NoError(t, xdr.SafeUnmarshalBase64(xdrB64, &env))
		maxTime := int64(env.Preconditions().TimeBounds.MaxTime)
		assert.GreaterOrEqual(t, maxTime, before+tc.want)
		assert.LessOrEqual(t, maxTime, time.Now().Unix()+tc.want)
	}
}

func TestClassicBuildAccountNotFound(t *testing.T) {
	t.Parallel()
	rpc := newFakeLedger(t, "", 0, false)
	src := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: "unused"}}, rpc)
	_, err := src.BuildTx(context.Background(), types.SwapBuildRequest{Network: types.PUBLIC,
		Account: keypair.MustRandom().Address(), FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer,
		Amount: "1", MinOut: "0.1", Fee: "0.00001"})
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
			FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100", MinOut: "17.5", Fee: "0.0000001",
		})
		require.NoError(t, err)
		var env xdr.TransactionEnvelope
		require.NoError(t, xdr.SafeUnmarshalBase64(xdrB64, &env))
		assert.Equal(t, int64(100*len(env.Operations())), int64(env.Fee()))
	}
}
