package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

type fakeSwapQuoteService struct {
	quote *types.SwapQuote
	err   error
	got   types.SwapQuoteRequest
}

func (f *fakeSwapQuoteService) Name() string { return "fake" }
func (f *fakeSwapQuoteService) GetBestQuote(_ context.Context, req types.SwapQuoteRequest) (*types.SwapQuote, error) {
	f.got = req
	return f.quote, f.err
}

func swapBody(overrides string) string {
	base := `{"sourceAsset":"XLM","destAsset":"USDC:` + validIssuer + `","sourceAmount":"10.5","sender":"` + testAddress + `","slippagePercent":1`
	if overrides != "" {
		base += "," + overrides
	}
	return base + "}"
}

func destBody(overrides string) string {
	return strings.Replace(swapBody(overrides), `"sourceAmount":"10.5"`, `"destAmount":"2.3"`, 1)
}

func callSwapQuote(t *testing.T, svc types.SwapQuoteService, network, body string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/swap/quote?network="+network, strings.NewReader(body))
	rr := httptest.NewRecorder()
	return rr, NewSwapQuoteHandler(svc).GetSwapQuote(rr, req)
}

func TestSwapQuote_Success(t *testing.T) {
	t.Parallel()
	svc := &fakeSwapQuoteService{quote: &types.SwapQuote{
		Source: types.SwapSourceXoxno, SourceAmount: "10.5", DestinationAmount: "1.6800000",
		Alternatives: []types.SwapQuoteAlternative{{Source: "horizon", DestinationAmount: "1.6700000"}, {Source: "xoxno", DestinationAmount: "1.6800000", Selected: true}},
	}}

	rr, err := callSwapQuote(t, svc, "PUBLIC", swapBody(""))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Data types.SwapQuote `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, "xoxno", resp.Data.Source)
	assert.Len(t, resp.Data.Alternatives, 2)

	assert.Equal(t, types.PUBLIC, svc.got.Network)
	assert.Equal(t, "10.5", svc.got.SourceAmount)
	assert.Equal(t, 7, svc.got.SourceDecimals, "classic precision by default")
	assert.Equal(t, int64(180), svc.got.TimeoutSeconds, "default transaction lifetime")
	assert.InDelta(t, 1.0, svc.got.SlippagePercent, 0)
}

func TestSwapQuote_PassesExplicitDecimalsAndTimeout(t *testing.T) {
	t.Parallel()
	sorobanSource := strings.Replace(swapBody(`"sourceDecimals":18,"timeoutSeconds":60`), `"sourceAsset":"XLM"`, `"sourceAsset":"`+testContractAddress+`"`, 1)
	sorobanDest := strings.Replace(destBody(`"destDecimals":18`), `USDC:`+validIssuer, testContractAddress, 1)
	cases := map[string]struct {
		network, body string
		check         func(t *testing.T, got types.SwapQuoteRequest)
	}{
		"soroban source decimals and timeout": {"TESTNET", sorobanSource, func(t *testing.T, got types.SwapQuoteRequest) {
			assert.Equal(t, 18, got.SourceDecimals)
			assert.Equal(t, testContractAddress, got.SourceAsset)
			assert.Equal(t, int64(60), got.TimeoutSeconds)
		}},
		"exact-out": {"PUBLIC", destBody(""), func(t *testing.T, got types.SwapQuoteRequest) {
			assert.Equal(t, "2.3", got.DestAmount)
			assert.Empty(t, got.SourceAmount)
			assert.Equal(t, 7, got.DestDecimals)
			assert.Equal(t, 7, got.SourceDecimals)
		}},
		"exact-out soroban destination decimals": {"PUBLIC", sorobanDest, func(t *testing.T, got types.SwapQuoteRequest) {
			assert.Equal(t, 18, got.DestDecimals)
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeSwapQuoteService{quote: &types.SwapQuote{}}
			_, err := callSwapQuote(t, svc, tc.network, tc.body)
			require.NoError(t, err)
			tc.check(t, svc.got)
		})
	}
}

func TestSwapQuote_RejectsBadInput(t *testing.T) {
	t.Parallel()
	replace := func(old, new string) string { return strings.Replace(swapBody(""), old, new, 1) }
	sorobanSource := func(overrides string) string {
		return strings.Replace(swapBody(overrides), `"sourceAsset":"XLM"`, `"sourceAsset":"`+testContractAddress+`"`, 1)
	}
	cases := map[string]struct{ network, body, wantMsg string }{
		"futurenet":                        {"FUTURENET", swapBody(""), "invalid network"},
		"no network":                       {"", swapBody(""), "invalid network"},
		"not json":                         {"PUBLIC", "nope", "invalid JSON"},
		"amount not a string":              {"PUBLIC", replace(`"10.5"`, `10.5`), "invalid JSON"},
		"bad sender":                       {"PUBLIC", replace(testAddress, "GNOTAKEY"), "sender must be a Stellar account address"},
		"contract sender":                  {"PUBLIC", replace(testAddress, testContractAddress), "sender must be a Stellar account address"},
		"same asset":                       {"PUBLIC", replace(`"destAsset":"USDC:`+validIssuer+`"`, `"destAsset":"XLM"`), "must differ"},
		"same asset alias":                 {"PUBLIC", replace(`"destAsset":"USDC:`+validIssuer+`"`, `"destAsset":"native"`), "must differ"},
		"bad dest asset":                   {"PUBLIC", replace(`"destAsset":"USDC:`+validIssuer+`"`, `"destAsset":"USDC:notakey"`), "invalid destAsset"},
		"classic decimals":                 {"PUBLIC", swapBody(`"sourceDecimals":6`), "sourceDecimals must be 7 for a classic asset"},
		"no source":                        {"PUBLIC", replace(`"sourceAsset":"XLM"`, `"sourceAsset":""`), "invalid sourceAsset"},
		"zero amount":                      {"PUBLIC", replace(`"10.5"`, `"0"`), "invalid sourceAmount"},
		"negative amount":                  {"PUBLIC", replace(`"10.5"`, `"-1"`), "invalid sourceAmount"},
		"too many decimals":                {"PUBLIC", replace(`"10.5"`, `"1.12345678"`), "more than 7 decimal places"},
		"no slippage":                      {"PUBLIC", replace(`,"slippagePercent":1`, ""), "slippagePercent must be between"},
		"slippage too big":                 {"PUBLIC", replace(`"slippagePercent":1`, `"slippagePercent":51`), "slippagePercent must be between"},
		"slippage tiny":                    {"PUBLIC", replace(`"slippagePercent":1`, `"slippagePercent":0.00001`), "slippagePercent must be between"},
		"timeout too long":                 {"PUBLIC", swapBody(`"timeoutSeconds":901`), "timeoutSeconds must be between"},
		"timeout negative":                 {"PUBLIC", swapBody(`"timeoutSeconds":-1`), "timeoutSeconds must be between"},
		"neither amount":                   {"PUBLIC", replace(`"sourceAmount":"10.5",`, ""), "exactly one of sourceAmount and destAmount"},
		"both amounts":                     {"PUBLIC", swapBody(`"destAmount":"2.3"`), "exactly one of sourceAmount and destAmount"},
		"classic dest decimals":            {"PUBLIC", destBody(`"destDecimals":6`), "destDecimals must be 7 for a classic asset"},
		"soroban source without decimals":  {"PUBLIC", sorobanSource(""), "sourceDecimals is required for a Soroban token"},
		"soroban dest without decimals":    {"PUBLIC", replace(`USDC:`+validIssuer, testContractAddress), "destDecimals is required for a Soroban token"},
		"soroban source decimals above 38": {"PUBLIC", sorobanSource(`"sourceDecimals":39`), "sourceDecimals must be between 0 and 38"},
		"soroban source decimals negative": {"PUBLIC", sorobanSource(`"sourceDecimals":-1`), "sourceDecimals must be between 0 and 38"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeSwapQuoteService{quote: &types.SwapQuote{}}
			_, err := callSwapQuote(t, svc, tc.network, tc.body)
			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, unwrapHttpStatus(t, err))
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.Empty(t, svc.got.Network, "a rejected request must not reach the service")
		})
	}
}

func TestSwapQuote_ServiceErrorsAreMapped(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		err  error
		want int
	}{
		"no route": {types.ErrSwapNoRoute, http.StatusNotFound},
		"timeout":  {context.DeadlineExceeded, http.StatusGatewayTimeout},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := callSwapQuote(t, &fakeSwapQuoteService{err: tc.err}, "PUBLIC", swapBody(""))
			require.Error(t, err)
			assert.Equal(t, tc.want, unwrapHttpStatus(t, err))
		})
	}
}
