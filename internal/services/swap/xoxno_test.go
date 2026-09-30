package swap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	// The quote server's own documented examples: the XLM and Circle USDC
	// Stellar Asset Contracts on pubnet.
	pubnetXlmSac  = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
	pubnetUsdcSac = "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75"
	pubnetPass    = "Public Global Stellar Network ; September 2015"
)

func TestSwapAsset_ContractIDDerivation(t *testing.T) {
	t.Parallel()
	xlm, err := parseAsset("XLM")
	require.NoError(t, err)
	got, err := xlm.contractIDFor(pubnetPass)
	require.NoError(t, err)
	assert.Equal(t, pubnetXlmSac, got)

	usdc, err := parseAsset("USDC:" + testIssuer)
	require.NoError(t, err)
	got, err = usdc.contractIDFor(pubnetPass)
	require.NoError(t, err)
	assert.Equal(t, pubnetUsdcSac, got)

	c := testContract(7)
	tok, err := parseAsset(c)
	require.NoError(t, err)
	got, err = tok.contractIDFor(pubnetPass)
	require.NoError(t, err)
	assert.Equal(t, c, got, "a contract token is its own id")
}

func TestParseSwapAsset_Rejects(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "USDC", "USDC:notakey", ":" + testIssuer, "TOOLONGASSETCODE:" + testIssuer, "C123"} {
		_, err := parseAsset(in)
		assert.Error(t, err, in)
	}
}

const (
	accountHoldsUSDC   = `{"sequence":"41","balances":[{"asset_type":"native"},{"asset_type":"credit_alphanum4","asset_code":"USDC","asset_issuer":"` + testIssuer + `"}]}`
	accountNoTrustline = `{"sequence":"41","balances":[{"asset_type":"native"}]}`
)

type xoxnoFixture struct {
	router string
	tx     map[string]any
	quote  map[string]any
	status int
	gotURL string

	account       string
	accountStatus int
}

func newXoxnoFixture(t *testing.T) (*xoxnoFixture, *xoxnoSource, types.SwapQuoteRequest) {
	t.Helper()
	router := testContract(2)
	o := envOpts{
		router: router, function: routerFunction, sender: testSender, amount: 100_0000000, fee: 500_000,
		payload: buildPayloadTokens(t, pubnetXlmSac, pubnetUsdcSac, 158400000),
	}
	f := &xoxnoFixture{router: router, status: http.StatusOK, account: accountHoldsUSDC, accountStatus: http.StatusOK}
	f.tx = map[string]any{
		"envelopeXdr":       buildEnvelope(t, o),
		"routerContract":    router,
		"networkPassphrase": pubnetPass,
		"simulated":         true,
	}
	f.quote = map[string]any{
		"mode": "forward", "from": pubnetXlmSac, "to": pubnetUsdcSac,
		"amountIn": "1000000000", "amountOut": "160000000", "amountOutMin": "158400000",
		"decimalsOut": 7, "priceImpact": 0.0012,
		"hops": []map[string]any{{"dex": "Soroswap", "kind": "ConstantProduct", "address": testContract(5), "from": pubnetXlmSac, "to": pubnetUsdcSac}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/accounts/") {
			w.WriteHeader(f.accountStatus)
			_, _ = w.Write([]byte(f.account))
			return
		}
		f.gotURL = r.URL.String()
		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			return
		}
		body := map[string]any{}
		for k, v := range f.quote {
			body[k] = v
		}
		body["transaction"] = f.tx
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)

	src := newXoxnoSource(map[string]Network{types.PUBLIC: {QuoteURL: srv.URL, Router: router}}, newHorizonClient(srv.URL, srv.URL), nil)
	src.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	req := types.SwapQuoteRequest{
		Network: types.PUBLIC, SourceAsset: "XLM", DestAsset: "USDC:" + testIssuer,
		SourceAmount: "100", SourceDecimals: 7, DestDecimals: 7, Sender: testSender, SlippagePercent: 1, TimeoutSeconds: 180,
	}
	return f, src, req
}

func TestXoxnoSource_QuotesAndPreparesTransaction(t *testing.T) {
	t.Parallel()
	f, src, req := newXoxnoFixture(t)

	c, err := src.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "160000000", c.DestAmount.String())
	assert.Equal(t, "158400000", c.DestAmountMin.String())
	assert.Equal(t, 7, c.DestDecimals)
	require.Len(t, c.Route, 1)
	assert.Equal(t, "Soroswap", c.Route[0].Venue)
	require.NotNil(t, c.Transaction)
	assert.Equal(t, f.router, c.Transaction.RouterContract)
	assert.True(t, c.Transaction.Simulated)
	assert.Equal(t, "500000", c.NetworkFee.String(), "the envelope's own fee")
	assert.NotEqual(t, f.tx["envelopeXdr"], c.Transaction.EnvelopeXDR, "sequence and expiry are stamped")

	assert.Contains(t, f.gotURL, "from="+pubnetXlmSac)
	assert.Contains(t, f.gotURL, "to="+pubnetUsdcSac)
	assert.Contains(t, f.gotURL, "amount_in=1000000000")
	assert.Contains(t, f.gotURL, "slippage=0.01")
	assert.Contains(t, f.gotURL, "sender="+testSender)
	assert.Contains(t, f.gotURL, "simulate=true")
}

// withEnvelope replaces the quote's transaction with one for amount and a route
// payload floored at minOut.
func withEnvelope(t *testing.T, f *xoxnoFixture, amount, minOut int64) {
	t.Helper()
	f.tx["envelopeXdr"] = buildEnvelope(t, envOpts{
		router: f.router, function: routerFunction, sender: testSender, amount: amount, fee: 500_000,
		payload: buildPayloadTokens(t, pubnetXlmSac, pubnetUsdcSac, minOut),
	})
}

func TestXoxnoSource_DropsQuotesThatDoNotMatchTheRequest(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, f *xoxnoFixture){
		"different input":                  func(t *testing.T, f *xoxnoFixture) { f.quote["amountIn"] = "1000000001" },
		"different pair":                   func(t *testing.T, f *xoxnoFixture) { f.quote["to"] = testContract(6) },
		"reverse mode":                     func(t *testing.T, f *xoxnoFixture) { f.quote["mode"] = "reverse" },
		"zero output":                      func(t *testing.T, f *xoxnoFixture) { f.quote["amountOut"] = "0" },
		"zero minimum":                     func(t *testing.T, f *xoxnoFixture) { f.quote["amountOutMin"] = "0" },
		"minimum above output":             func(t *testing.T, f *xoxnoFixture) { f.quote["amountOutMin"] = "160000001" },
		"minimum below the slippage floor": func(t *testing.T, f *xoxnoFixture) { f.quote["amountOutMin"] = "158399999" },
		"garbage output":                   func(t *testing.T, f *xoxnoFixture) { f.quote["amountOut"] = "lots" },
		"missing decimals":                 func(t *testing.T, f *xoxnoFixture) { delete(f.quote, "decimalsOut") },
		"classic wrong scale":              func(t *testing.T, f *xoxnoFixture) { f.quote["decimalsOut"] = 6 },
		"not simulated":                    func(t *testing.T, f *xoxnoFixture) { f.tx["simulated"] = false },
		"other router":                     func(t *testing.T, f *xoxnoFixture) { f.tx["routerContract"] = testContract(8) },
		"other network":                    func(t *testing.T, f *xoxnoFixture) { f.tx["networkPassphrase"] = "Test SDF Network ; September 2015" },
		"no envelope":                      func(t *testing.T, f *xoxnoFixture) { f.tx["envelopeXdr"] = "" },
		"malformed envelope":               func(t *testing.T, f *xoxnoFixture) { f.tx["envelopeXdr"] = "AAAA" },
		"envelope for another amount":      func(t *testing.T, f *xoxnoFixture) { withEnvelope(t, f, 200_0000000, 158400000) },
		"payload below the quoted minimum": func(t *testing.T, f *xoxnoFixture) { withEnvelope(t, f, 100_0000000, 1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, src, req := newXoxnoFixture(t)
			mutate(t, f)
			_, err := src.Quote(context.Background(), req)
			assert.ErrorIs(t, err, errInvalidQuote)
		})
	}
}

func TestXoxnoSource_UpstreamStatuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"not found is no route", http.StatusNotFound, types.ErrSwapNoRoute},
		{"unprocessable is no route", http.StatusUnprocessableEntity, types.ErrSwapNoRoute},
		{"bad request is unsupported", http.StatusBadRequest, errUnsupported},
		{"bad gateway is an upstream failure", http.StatusBadGateway, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, src, req := newXoxnoFixture(t)
			f.status = tc.status
			_, err := src.Quote(context.Background(), req)
			require.Error(t, err)
			if tc.want != nil {
				assert.ErrorIs(t, err, tc.want)
				return
			}
			assert.NotErrorIs(t, err, types.ErrSwapNoRoute)
			assert.NotErrorIs(t, err, errUnsupported)
		})
	}
}

func TestXoxnoSource_UnfundedSenderIsUnsupported(t *testing.T) {
	t.Parallel()
	f, src, req := newXoxnoFixture(t)
	f.accountStatus = http.StatusNotFound
	_, err := src.Quote(context.Background(), req)
	assert.ErrorIs(t, err, errUnsupported)
}

func TestXoxnoSource_DestinationWithoutTrustlineIsQuotedWithoutATransaction(t *testing.T) {
	t.Parallel()
	f, src, req := newXoxnoFixture(t)
	f.account = accountNoTrustline
	delete(f.tx, "envelopeXdr")

	c, err := src.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, c.RequiresTrustline)
	assert.Nil(t, c.Transaction, "nothing to sign until the trustline exists")
	assert.Nil(t, c.NetworkFee)
	assert.Equal(t, "160000000", c.DestAmount.String())
	assert.Contains(t, f.gotURL, "simulate=false", "the aggregator cannot simulate a payout the account cannot receive")
}

func TestXoxnoSource_UnconfiguredNetworkIsUnsupported(t *testing.T) {
	t.Parallel()
	_, src, req := newXoxnoFixture(t)
	req.Network = types.TESTNET
	_, err := src.Quote(context.Background(), req)
	assert.ErrorIs(t, err, errUnsupported)
}

func TestXoxnoSource_SorobanTokenUsesRequestedDecimals(t *testing.T) {
	t.Parallel()
	f, src, req := newXoxnoFixture(t)
	tok := testContract(6)
	req.DestAsset = tok
	req.DestDecimals = 18
	f.quote["to"] = tok
	f.quote["decimalsOut"] = 18
	f.quote["amountOut"] = "160000000000"
	f.quote["amountOutMin"] = "158400000000"
	o := envOpts{
		router: f.router, function: routerFunction, sender: testSender, amount: 100_0000000, fee: 500_000,
		payload: buildPayloadTokens(t, pubnetXlmSac, tok, 158400000000),
	}
	f.tx["envelopeXdr"] = buildEnvelope(t, o)

	c, err := src.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 18, c.DestDecimals)
	assert.Equal(t, "160000000000", c.DestAmount.String())

	req.DestDecimals = 7
	_, err = src.Quote(context.Background(), req)
	assert.ErrorIs(t, err, errInvalidQuote, "the output precision must match the request")
}

func buildPayloadTokens(t *testing.T, in, out string, minOut int64) []byte {
	t.Helper()
	return buildPayload(t, payloadOpts{version: 1, tokenIn: in, tokenOut: out, minOut: minOut})
}

func newReverseFixture(t *testing.T) (*xoxnoFixture, *xoxnoSource, types.SwapQuoteRequest) {
	t.Helper()
	f, src, req := newXoxnoFixture(t)
	f.quote = map[string]any{
		"mode": "reverse", "from": pubnetXlmSac, "to": pubnetUsdcSac,
		"amountIn": "100489927", "amountOut": "23000002", "amountOutMin": "23000000", "decimalsOut": 7,
	}
	req.SourceAmount = ""
	req.DestAmount, req.DestDecimals = "2.3", 7
	return f, src, req
}

func TestXoxnoSource_QuoteInputSizesTheInputWithoutASenderOrSimulation(t *testing.T) {
	t.Parallel()
	f, src, req := newReverseFixture(t)

	in, err := src.QuoteInput(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "100489927", in.String())
	assert.Contains(t, f.gotURL, "amount_out=23000000")
	assert.Contains(t, f.gotURL, "from="+pubnetXlmSac)
	assert.NotContains(t, f.gotURL, "sender=", "sizing needs no account and no simulation")
	assert.NotContains(t, f.gotURL, "amount_in=")
}

func TestXoxnoSource_QuoteInputDropsAnswersToAnotherQuestion(t *testing.T) {
	t.Parallel()
	cases := map[string]func(f *xoxnoFixture){
		"forward mode":     func(f *xoxnoFixture) { f.quote["mode"] = "forward" },
		"another pair":     func(f *xoxnoFixture) { f.quote["to"] = testContract(6) },
		"zero input":       func(f *xoxnoFixture) { f.quote["amountIn"] = "0" },
		"garbage input":    func(f *xoxnoFixture) { f.quote["amountIn"] = "lots" },
		"output below ask": func(f *xoxnoFixture) { f.quote["amountOut"] = "22999999" },
		"garbage output":   func(f *xoxnoFixture) { f.quote["amountOut"] = "x" },
		"wrong decimals":   func(f *xoxnoFixture) { f.quote["decimalsOut"] = 6 },
		"missing decimals": func(f *xoxnoFixture) { delete(f.quote, "decimalsOut") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, src, req := newReverseFixture(t)
			mutate(f)
			_, err := src.QuoteInput(context.Background(), req)
			assert.ErrorIs(t, err, errInvalidQuote)
		})
	}
}

func TestXoxnoSource_QuoteInputRejectsSorobanOutputWithDifferentDecimals(t *testing.T) {
	t.Parallel()
	f, src, req := newReverseFixture(t)
	req.DestAsset, req.DestDecimals = testContract(6), 18
	f.quote["to"] = req.DestAsset
	f.quote["amountOut"] = "2300000000000000000"

	_, err := src.QuoteInput(context.Background(), req)
	assert.ErrorIs(t, err, errInvalidQuote)
}

func TestXoxnoSource_QuoteInputStatusesAndUnconfiguredNetwork(t *testing.T) {
	t.Parallel()
	f, src, req := newReverseFixture(t)
	f.status = http.StatusNotFound
	_, err := src.QuoteInput(context.Background(), req)
	assert.ErrorIs(t, err, types.ErrSwapNoRoute)

	req.Network = types.TESTNET
	_, err = src.QuoteInput(context.Background(), req)
	assert.ErrorIs(t, err, errUnsupported)
}
