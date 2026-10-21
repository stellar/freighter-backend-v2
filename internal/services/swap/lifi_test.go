package swap

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
			require.ErrorIs(t, err, errLifiMissingTx)
			assert.Equal(t, "error", Reason(err))

			r := NewRouter([]Source{src, &fakeSource{name: "classic", route: route("classic", "10", "1.8")}}, time.Second, nil)
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
