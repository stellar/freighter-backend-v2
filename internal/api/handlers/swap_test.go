package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/services/swap"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	swapAccount  = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	swapNativeID = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
	swapUSDC     = "USDC:" + swapAccount
)

type fakeSwapService struct {
	quote    types.SwapQuotesResponse
	quoteErr error
	build    types.SwapBuildResponse
	buildErr error
	lastQ    types.SwapQuoteRequest
	lastB    types.SwapBuildRequest
	calls    int
}

func (f *fakeSwapService) Quote(_ context.Context, req types.SwapQuoteRequest) (types.SwapQuotesResponse, error) {
	f.calls++
	f.lastQ = req
	return f.quote, f.quoteErr
}

func (f *fakeSwapService) Build(_ context.Context, req types.SwapBuildRequest) (types.SwapBuildResponse, error) {
	f.calls++
	f.lastB = req
	return f.build, f.buildErr
}

var testnetNativeID = func() string {
	id, err := swap.Asset{ID: "XLM"}.ContractID(types.TESTNET)
	if err != nil {
		panic(err)
	}
	return id
}()

func swapQuoteBody(mode, from, to, amount string) string {
	return fmt.Sprintf(`{"account":%q,"mode":%q,"fromAsset":%q,"toAsset":%q,"amount":%q}`, swapAccount, mode, from, to, amount)
}

func swapBuildBody(from, to, amount string) string {
	return fmt.Sprintf(`{"account":%q,"source":"classic","fromAsset":%q,"toAsset":%q,"amount":%q,"minOut":"9.5","fee":"0.00001"}`, swapAccount, from, to, amount)
}

func doQuotes(h *SwapHandler, network, body string) (*httptest.ResponseRecorder, error) {
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/swap/quotes?network="+network, strings.NewReader(body))
	rr := httptest.NewRecorder()
	return rr, h.Quotes(rr, req)
}

func doBuild(h *SwapHandler, network, body string) (*httptest.ResponseRecorder, error) {
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/swap/tx?network="+network, strings.NewReader(body))
	rr := httptest.NewRecorder()
	return rr, h.Build(rr, req)
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var he *httperror.HttpError
	require.ErrorAs(t, err, &he)
	return he.HttpStatus()
}

func TestSwapQuotes_Success(t *testing.T) {
	t.Parallel()
	f := &fakeSwapService{quote: types.SwapQuotesResponse{
		Routes:      []types.SwapRoute{{Source: "classic", FromAmount: "10", ToAmount: "1"}},
		Unavailable: []types.SwapUnavailable{{Source: "lifi", Reason: "timeout"}},
	}}
	rr, err := doQuotes(NewSwapHandler(f), "PUBLIC", swapQuoteBody("send", "XLM", swapUSDC, "10"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data types.SwapQuotesResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Len(t, resp.Data.Routes, 1)
	assert.Len(t, resp.Data.Unavailable, 1)
	assert.Equal(t, "PUBLIC", f.lastQ.Network)
	assert.Equal(t, "send", f.lastQ.Mode)
}

func TestSwapQuotes_EmptySlicesSerializeAsArrays(t *testing.T) {
	t.Parallel()
	f := &fakeSwapService{quote: types.SwapQuotesResponse{Routes: []types.SwapRoute{}, Unavailable: []types.SwapUnavailable{}}}
	rr, err := doQuotes(NewSwapHandler(f), "PUBLIC", swapQuoteBody("receive", "XLM", swapUSDC, "1"))
	require.NoError(t, err)
	assert.Contains(t, rr.Body.String(), `"routes":[]`)
	assert.Contains(t, rr.Body.String(), `"unavailable":[]`)
}

func TestSwapQuotes_Validation(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		network, body string
	}{
		"invalid network":       {"MARS", swapQuoteBody("send", "XLM", swapUSDC, "10")},
		"missing network":       {"", swapQuoteBody("send", "XLM", swapUSDC, "10")},
		"bad account":           {"PUBLIC", strings.Replace(swapQuoteBody("send", "XLM", swapUSDC, "10"), swapAccount+`","mode`, `nope","mode`, 1)},
		"bad from asset":        {"PUBLIC", swapQuoteBody("send", "???", swapUSDC, "10")},
		"bad to asset":          {"PUBLIC", swapQuoteBody("send", "XLM", "USDC", "10")},
		"missing mode":          {"PUBLIC", swapQuoteBody("", "XLM", swapUSDC, "10")},
		"unknown mode":          {"PUBLIC", swapQuoteBody("sell", "XLM", swapUSDC, "10")},
		"same asset":            {"PUBLIC", swapQuoteBody("send", "XLM", "XLM", "10")},
		"native vs contract id": {"PUBLIC", swapQuoteBody("send", "XLM", swapNativeID, "10")},
		"amount zero":           {"PUBLIC", swapQuoteBody("send", "XLM", swapUSDC, "0")},
		"amount negative":       {"PUBLIC", swapQuoteBody("send", "XLM", swapUSDC, "-1")},
		"amount garbage":        {"PUBLIC", swapQuoteBody("send", "XLM", swapUSDC, "abc")},
		"amount too precise":    {"PUBLIC", swapQuoteBody("send", "XLM", swapUSDC, "1.12345678")},
		"amount over int64":     {"PUBLIC", swapQuoteBody("send", "XLM", swapUSDC, "922337203685.4775808")},
		"body not json":         {"PUBLIC", "{"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := &fakeSwapService{}
			_, err := doQuotes(NewSwapHandler(f), tc.network, tc.body)
			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, statusOf(t, err))
			assert.Zero(t, f.calls)
		})
	}
}

func TestSwapQuotes_AmountBoundaries(t *testing.T) {
	t.Parallel()
	f := &fakeSwapService{}
	_, err := doQuotes(NewSwapHandler(f), "PUBLIC", swapQuoteBody("send", "XLM", swapUSDC, "922337203685.4775807"))
	require.NoError(t, err)
	// A non-classic asset's decimals are only known to the source, so the
	// handler leaves the precision to it.
	_, err = doQuotes(NewSwapHandler(f), "PUBLIC", swapQuoteBody("send", swapNativeID, swapUSDC, "1.1234567890123456789012345678901234567"))
	require.NoError(t, err)
}

func TestSwapQuotes_ErrorMapping(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		err  error
		want int
	}{
		"all failed":    {swap.ErrAllSourcesFailed, http.StatusBadGateway},
		"bad request":   {swap.ErrBadRequest, http.StatusBadRequest},
		"deadline":      {context.DeadlineExceeded, http.StatusServiceUnavailable},
		"canceled":      {context.Canceled, http.StatusServiceUnavailable},
		"other":         {errors.New("boom"), http.StatusInternalServerError},
		"wrapped moved": {fmt.Errorf("x: %w", swap.ErrQuoteMoved), http.StatusConflict},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := &fakeSwapService{quoteErr: tc.err}
			_, err := doQuotes(NewSwapHandler(f), "PUBLIC", swapQuoteBody("send", "XLM", swapUSDC, "10"))
			require.Error(t, err)
			assert.Equal(t, tc.want, statusOf(t, err))
		})
	}
}

func TestSwapQuotes_BodyTooLarge(t *testing.T) {
	t.Parallel()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/swap/quotes?network=PUBLIC", strings.NewReader(swapQuoteBody("send", "XLM", swapUSDC, "10")))
	rr := httptest.NewRecorder()
	req.Body = http.MaxBytesReader(rr, req.Body, 5)
	err := NewSwapHandler(&fakeSwapService{}).Quotes(rr, req)
	require.Error(t, err)
	assert.Equal(t, http.StatusRequestEntityTooLarge, statusOf(t, err))
}

func TestSwapBuild_Success(t *testing.T) {
	t.Parallel()
	f := &fakeSwapService{build: types.SwapBuildResponse{XDR: "AAAA", AddsTrustline: true}}
	rr, err := doBuild(NewSwapHandler(f), "TESTNET", swapBuildBody("XLM", swapUSDC, "10"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data types.SwapBuildResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, "AAAA", resp.Data.XDR)
	assert.True(t, resp.Data.AddsTrustline)
	assert.Equal(t, "TESTNET", f.lastB.Network)
	assert.Equal(t, "classic", f.lastB.Source)
}

func TestSwapBuild_Validation(t *testing.T) {
	t.Parallel()
	good := swapBuildBody("XLM", swapUSDC, "10")
	cases := map[string]string{
		"missing source": strings.Replace(good, `"source":"classic"`, `"source":""`, 1),
		"bad minOut":     strings.Replace(good, `"minOut":"9.5"`, `"minOut":"x"`, 1),
		"zero minOut":    strings.Replace(good, `"minOut":"9.5"`, `"minOut":"0"`, 1),
		"fee over int64": strings.Replace(good, `"fee":"0.00001"`, `"fee":"922337203685.4775808"`, 1),
		"bad fee":        strings.Replace(good, `"fee":"0.00001"`, `"fee":"x"`, 1),
		"same asset":     swapBuildBody("XLM", testnetNativeID, "10"),
		"zero amount":    swapBuildBody("XLM", swapUSDC, "0"),
		"over int64":     swapBuildBody("XLM", swapUSDC, "922337203685.4775808"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := &fakeSwapService{}
			_, err := doBuild(NewSwapHandler(f), "TESTNET", body)
			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, statusOf(t, err))
			assert.Zero(t, f.calls)
		})
	}
}

func TestSwapBuild_Slippage(t *testing.T) {
	t.Parallel()
	good := swapBuildBody("XLM", swapUSDC, "10")
	withSlippage := func(v string) string {
		return strings.Replace(good, `"fee"`, `"slippage":`+v+`,"fee"`, 1)
	}
	for _, v := range []string{`"11"`, `"-1"`, `"abc"`, `"1e1"`, `"1/2"`, `"10.01"`, `"1,5"`, `".5"`, `"1."`} {
		t.Run("rejects "+v, func(t *testing.T) {
			t.Parallel()
			f := &fakeSwapService{}
			_, err := doBuild(NewSwapHandler(f), "TESTNET", withSlippage(v))
			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, statusOf(t, err))
			assert.Contains(t, err.Error(), "invalid slippage")
			assert.Zero(t, f.calls)
		})
	}
	for _, v := range []string{"0", "0.5", "1.5", "2", "10", "2.0"} {
		t.Run("accepts "+v, func(t *testing.T) {
			t.Parallel()
			f := &fakeSwapService{}
			_, err := doBuild(NewSwapHandler(f), "TESTNET", withSlippage(`"`+v+`"`))
			require.NoError(t, err)
			assert.Equal(t, v, f.lastB.Slippage)
		})
	}
	t.Run("absent stays empty", func(t *testing.T) {
		t.Parallel()
		f := &fakeSwapService{}
		_, err := doBuild(NewSwapHandler(f), "TESTNET", good)
		require.NoError(t, err)
		assert.Empty(t, f.lastB.Slippage)
		assert.Equal(t, 1, f.calls)
	})
}

func TestSwapBuild_Timeout(t *testing.T) {
	t.Parallel()
	good := swapBuildBody("XLM", swapUSDC, "10")
	withTimeout := func(v string) string {
		return strings.Replace(good, `"fee"`, `"timeout":`+v+`,"fee"`, 1)
	}
	for _, v := range []string{"0", "-1", "86401"} {
		t.Run("rejects "+v, func(t *testing.T) {
			t.Parallel()
			f := &fakeSwapService{}
			_, err := doBuild(NewSwapHandler(f), "TESTNET", withTimeout(v))
			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, statusOf(t, err))
			assert.Contains(t, err.Error(), "invalid timeout")
			assert.Zero(t, f.calls)
		})
	}
	for _, v := range []int64{1, 180, 86400} {
		t.Run(fmt.Sprint("accepts ", v), func(t *testing.T) {
			t.Parallel()
			f := &fakeSwapService{}
			_, err := doBuild(NewSwapHandler(f), "TESTNET", withTimeout(fmt.Sprint(v)))
			require.NoError(t, err)
			require.NotNil(t, f.lastB.Timeout)
			assert.Equal(t, v, *f.lastB.Timeout)
		})
	}
	t.Run("absent stays nil", func(t *testing.T) {
		t.Parallel()
		f := &fakeSwapService{}
		_, err := doBuild(NewSwapHandler(f), "TESTNET", good)
		require.NoError(t, err)
		assert.Nil(t, f.lastB.Timeout)
		assert.Equal(t, 1, f.calls)
	})
}

func TestSwapBuild_ErrorMapping(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		err  error
		want int
	}{
		"quote moved":          {swap.ErrQuoteMoved, http.StatusConflict},
		"no route":             {swap.ErrNoRoute, http.StatusConflict},
		"insufficient balance": {swap.ErrInsufficientBalance, http.StatusConflict},
		"check failed":         {swap.ErrCheckFailed, http.StatusBadGateway},
		"lifi missing tx":      {swap.ErrLifiMissingTx, http.StatusBadGateway},
		"account not found":    {swap.ErrAccountNotFound, http.StatusBadRequest},
		"bad request":          {swap.ErrBadRequest, http.StatusBadRequest},
		"all failed":           {swap.ErrAllSourcesFailed, http.StatusBadGateway},
		"upstream error":       {fmt.Errorf("lifi: %w", &metrics.UpstreamError{Kind: "http_error", Code: 424, Err: errors.New("x")}), http.StatusBadGateway},
		"rate limited":         {swap.ErrRateLimited, http.StatusBadGateway},
		"deadline":             {context.DeadlineExceeded, http.StatusServiceUnavailable},
		"other":                {errors.New("boom"), http.StatusInternalServerError},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := &fakeSwapService{buildErr: tc.err}
			_, err := doBuild(NewSwapHandler(f), "TESTNET", swapBuildBody("XLM", swapUSDC, "10"))
			require.Error(t, err)
			assert.Equal(t, tc.want, statusOf(t, err))
		})
	}
}

func TestSwapBuild_AccountNotFoundMessage(t *testing.T) {
	t.Parallel()
	f := &fakeSwapService{buildErr: swap.ErrAccountNotFound}
	_, err := doBuild(NewSwapHandler(f), "TESTNET", swapBuildBody("XLM", swapUSDC, "10"))
	var he *httperror.HttpError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, http.StatusBadRequest, he.HttpStatus())
	assert.Contains(t, he.Message, "account not found")
}
