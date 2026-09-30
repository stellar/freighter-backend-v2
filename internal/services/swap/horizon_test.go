package swap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

const testSender = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"

// xlmUsdc is a valid forward request for the classic pair.
var xlmUsdc = types.SwapQuoteRequest{
	Network: types.PUBLIC, SourceAsset: "XLM", DestAsset: "USDC:" + testIssuer,
	SourceAmount: "1", SourceDecimals: 7, SlippagePercent: 1,
}

func newHorizonServer(t *testing.T, handler http.HandlerFunc) *horizonClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return newHorizonClient(srv.URL, srv.URL)
}

func TestHorizonSource_QuotesBestPath(t *testing.T) {
	t.Parallel()
	var gotQuery map[string][]string
	client := newHorizonServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/paths/strict-send", r.URL.Path)
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{"_embedded":{"records":[{"destination_amount":"12.3456789","path":[{"asset_type":"native"},{"asset_type":"credit_alphanum4","asset_code":"EURC","asset_issuer":"` + testIssuer + `"}]}]}}`))
	})
	src := newHorizonSource(client, nil)

	c, err := src.Quote(context.Background(), types.SwapQuoteRequest{
		Network: types.PUBLIC, SourceAsset: "XLM", DestAsset: "USDC:" + testIssuer,
		SourceAmount: "100", SourceDecimals: 7, SlippagePercent: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, "123456789", c.DestAmount.String())
	assert.Equal(t, "122222221", c.DestAmountMin.String())
	assert.Equal(t, []string{"native", "EURC:" + testIssuer}, c.Path)

	assert.Equal(t, []string{"native"}, gotQuery["source_asset_type"])
	assert.Equal(t, []string{"100"}, gotQuery["source_amount"])
	assert.Equal(t, []string{"USDC:" + testIssuer}, gotQuery["destination_assets"])
	assert.Equal(t, []string{"1"}, gotQuery["limit"])
}

func TestHorizonSource_CreditSourceParams(t *testing.T) {
	t.Parallel()
	var gotQuery map[string][]string
	client := newHorizonServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{"_embedded":{"records":[]}}`))
	})
	src := newHorizonSource(client, nil)

	_, err := src.Quote(context.Background(), types.SwapQuoteRequest{
		Network: types.TESTNET, SourceAsset: "LONGCODE99:" + testIssuer, DestAsset: "XLM",
		SourceAmount: "1", SourceDecimals: 7, SlippagePercent: 1,
	})
	assert.ErrorIs(t, err, types.ErrSwapNoRoute)
	assert.Equal(t, []string{"credit_alphanum12"}, gotQuery["source_asset_type"])
	assert.Equal(t, []string{"LONGCODE99"}, gotQuery["source_asset_code"])
	assert.Equal(t, []string{testIssuer}, gotQuery["source_asset_issuer"])
	assert.Equal(t, []string{"native"}, gotQuery["destination_assets"])
}

func TestHorizonSource_ContractTokensAreUnsupported(t *testing.T) {
	t.Parallel()
	contract := testContract(1)
	cases := map[string]func(r *types.SwapQuoteRequest){
		"contract source":      func(r *types.SwapQuoteRequest) { r.SourceAsset = contract },
		"contract destination": func(r *types.SwapQuoteRequest) { r.DestAsset = contract },
		"non-classic decimals": func(r *types.SwapQuoteRequest) { r.SourceDecimals = 6 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			req := xlmUsdc
			mutate(&req)
			var called atomic.Bool
			client := newHorizonServer(t, func(w http.ResponseWriter, r *http.Request) { called.Store(true) })

			_, err := newHorizonSource(client, nil).Quote(context.Background(), req)
			assert.ErrorIs(t, err, errUnsupported)
			assert.False(t, called.Load(), "an unsupported pair must not reach Horizon")
		})
	}
}

func TestHorizonSource_UnusableAnswers(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		handler http.HandlerFunc
		noRoute bool
	}{
		"a zero destination is no route": {func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"_embedded":{"records":[{"destination_amount":"0.0000000","path":[]}]}}`))
		}, true},
		"an upstream error is not no route": {func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := newHorizonSource(newHorizonServer(t, tc.handler), nil).Quote(context.Background(), xlmUsdc)
			require.Error(t, err)
			assert.Equal(t, tc.noRoute, errors.Is(err, types.ErrSwapNoRoute))
		})
	}
}

func TestHorizonClient_Account(t *testing.T) {
	t.Parallel()
	client := newHorizonServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts/" + testSender:
			_, _ = w.Write([]byte(`{"sequence":"123456789012","balances":[{"asset_type":"native"},{"asset_type":"credit_alphanum4","asset_code":"USDC","asset_issuer":"` + testIssuer + `"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	acct, err := client.account(context.Background(), types.PUBLIC, testSender)
	require.NoError(t, err)
	assert.Equal(t, int64(123456789012), acct.Sequence)

	usdc, _ := parseAsset("USDC:" + testIssuer)
	eurc, _ := parseAsset("EURC:" + testIssuer)
	xlm, _ := parseAsset("XLM")
	assert.True(t, holdsAsset(acct, usdc))
	assert.False(t, holdsAsset(acct, eurc), "no trustline")
	assert.True(t, holdsAsset(acct, xlm))

	_, err = client.account(context.Background(), types.PUBLIC, testIssuer)
	assert.ErrorIs(t, err, errHorizonAccountNotFound)
}

func TestHorizonSource_QuoteInputAsksForTheCheapestPathToTheAmount(t *testing.T) {
	t.Parallel()
	var gotPath string
	var gotQuery map[string][]string
	client := newHorizonServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query()
		_, _ = w.Write([]byte(`{"_embedded":{"records":[{"source_amount":"10.0587578","destination_amount":"2.3000000","path":[]},{"source_amount":"10.0607071","destination_amount":"2.3000000","path":[]}]}}`))
	})
	src := newHorizonSource(client, nil)

	in, err := src.QuoteInput(context.Background(), types.SwapQuoteRequest{
		Network: types.PUBLIC, SourceAsset: "XLM", DestAsset: "USDC:" + testIssuer,
		DestAmount: "2.3", DestDecimals: 7, SourceDecimals: 7,
	})
	require.NoError(t, err)
	assert.Equal(t, "100587578", in.String(), "the first record is the cheapest")
	assert.Equal(t, "/paths/strict-receive", gotPath)
	assert.Equal(t, []string{"2.3"}, gotQuery["destination_amount"])
	assert.Equal(t, []string{"native"}, gotQuery["source_assets"])
	assert.Equal(t, []string{"credit_alphanum4"}, gotQuery["destination_asset_type"])
	assert.Equal(t, []string{"USDC"}, gotQuery["destination_asset_code"])
	assert.Equal(t, []string{testIssuer}, gotQuery["destination_asset_issuer"])
}

func TestHorizonSource_QuoteInputNoRouteAndUnsupported(t *testing.T) {
	t.Parallel()
	var called atomic.Bool
	client := newHorizonServer(t, func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		_, _ = w.Write([]byte(`{"_embedded":{"records":[]}}`))
	})
	src := newHorizonSource(client, nil)
	base := types.SwapQuoteRequest{Network: types.PUBLIC, SourceAsset: "XLM", DestAsset: "USDC:" + testIssuer, DestAmount: "1", DestDecimals: 7, SourceDecimals: 7}

	_, err := src.QuoteInput(context.Background(), base)
	assert.ErrorIs(t, err, types.ErrSwapNoRoute)

	called.Store(false)
	soroban := base
	soroban.DestAsset, soroban.DestDecimals = testContract(1), 18
	_, err = src.QuoteInput(context.Background(), soroban)
	assert.ErrorIs(t, err, errUnsupported)
	assert.False(t, called.Load(), "an unsupported pair must not reach Horizon")
}

func TestHorizonClient_ToleratesATrailingSlashInTheBaseURL(t *testing.T) {
	t.Parallel()
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		_, _ = w.Write([]byte(`{"sequence":"1","balances":[],"_embedded":{"records":[]}}`))
	}))
	t.Cleanup(srv.Close)
	client := newHorizonClient(srv.URL+"/", srv.URL+"/")

	_, err := client.account(context.Background(), types.PUBLIC, testSender)
	require.NoError(t, err)
	src, _ := parseAsset("XLM")
	dst, _ := parseAsset("USDC:" + testIssuer)
	_, err = client.strictSendBest(context.Background(), types.PUBLIC, src, dst, "1")
	require.NoError(t, err)

	assert.Equal(t, []string{"/accounts/" + testSender, "/paths/strict-send"}, gotPaths, "no doubled slash")
}
