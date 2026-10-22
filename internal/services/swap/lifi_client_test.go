package swap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

func lifiFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return b
}

func TestLifiClient_Quote(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_usdc_trust.json")
	var want LifiQuote
	require.NoError(t, json.Unmarshal(body, &want))
	require.NotNil(t, want.TransactionRequest)
	require.NotEmpty(t, want.TransactionRequest.Data)

	var gotKey, gotPath string
	var gotQuery map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-lifi-api-key")
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := NewLifiClient(srv.URL, "test-key", srv.Client())
	q, err := c.Quote(context.Background(), "CFROM", "CTO", "100", "GADDR", "0.02")
	require.NoError(t, err)
	assert.Equal(t, want.Action.FromAmount, q.Action.FromAmount)
	assert.Equal(t, want.Estimate.ToAmount, q.Estimate.ToAmount)
	assert.Equal(t, want.TransactionRequest.Data, q.TransactionRequest.Data)
	assert.Equal(t, "test-key", gotKey)
	assert.Equal(t, "/v1/quote", gotPath)
	assert.Equal(t, []string{LifiStellarChainID}, gotQuery["fromChain"])
	assert.Equal(t, []string{LifiStellarChainID}, gotQuery["toChain"])
	assert.Equal(t, []string{"CFROM"}, gotQuery["fromToken"])
	assert.Equal(t, []string{"CTO"}, gotQuery["toToken"])
	assert.Equal(t, []string{"100"}, gotQuery["fromAmount"])
	assert.Equal(t, []string{"GADDR"}, gotQuery["fromAddress"])
	assert.Equal(t, []string{"0.02"}, gotQuery["slippage"])
}

func TestLifiClient_NoKeyHeaderWhenUnset(t *testing.T) {
	var has bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, has = r.Header["X-Lifi-Api-Key"]
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	_, err := NewLifiClient(srv.URL, "", srv.Client()).Quote(context.Background(), "a", "b", "1", "c", "0.005")
	require.NoError(t, err)
	assert.False(t, has)
}

func TestLifiClient_Errors(t *testing.T) {
	notFound := lifiFixture(t, "lifi_err_1003.json")
	tests := []struct {
		name   string
		status int
		body   []byte
		check  func(t *testing.T, err error)
	}{
		{"1003 is no route", 404, notFound, func(t *testing.T, err error) { assert.ErrorIs(t, err, ErrNoRoute) }},
		{"1002 is no route", 404, []byte(`{"code":1002,"message":"x"}`), func(t *testing.T, err error) { assert.ErrorIs(t, err, ErrNoRoute) }},
		{"1011 is no route", 404, []byte(`{"code":1011,"message":"x"}`), func(t *testing.T, err error) { assert.ErrorIs(t, err, ErrNoRoute) }},
		{"other 404 is upstream error", 404, []byte(`{"code":9999}`), func(t *testing.T, err error) {
			var ue *metrics.UpstreamError
			require.True(t, errors.As(err, &ue))
			assert.Equal(t, 404, ue.Code)
		}},
		{"429 is rate limited", 429, []byte(`{}`), func(t *testing.T, err error) { assert.ErrorIs(t, err, ErrRateLimited) }},
		{"500 is upstream error", 500, []byte(`oops`), func(t *testing.T, err error) {
			var ue *metrics.UpstreamError
			require.True(t, errors.As(err, &ue))
			assert.Equal(t, "http_error", ue.Kind)
			assert.Equal(t, 500, ue.Code)
		}},
		{"424 is upstream error", 424, []byte(`{"message":"simulation failed"}`), func(t *testing.T, err error) {
			var ue *metrics.UpstreamError
			require.True(t, errors.As(err, &ue))
			assert.Equal(t, 424, ue.Code)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write(tt.body)
			}))
			defer srv.Close()
			_, err := NewLifiClient(srv.URL, "k", srv.Client()).Quote(context.Background(), "a", "b", "1", "c", "0.005")
			require.Error(t, err)
			tt.check(t, err)
		})
	}
}

func TestLifiClient_TokenDecimals(t *testing.T) {
	body := lifiFixture(t, "lifi_token_dejtrsy.json")
	var tok struct {
		Decimals int `json:"decimals"`
	}
	require.NoError(t, json.Unmarshal(body, &tok))
	require.Equal(t, 18, tok.Decimals)

	var calls int32
	var gotQuery map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		gotQuery = r.URL.Query()
		assert.Equal(t, "/v1/token", r.URL.Path)
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := NewLifiClient(srv.URL, "k", srv.Client())
	for i := 0; i < 2; i++ {
		d, err := c.TokenDecimals(context.Background(), "CTOKEN")
		require.NoError(t, err)
		assert.Equal(t, 18, d)
	}
	assert.EqualValues(t, 1, atomic.LoadInt32(&calls))
	assert.Equal(t, []string{LifiStellarChainID}, gotQuery["chain"])
	assert.Equal(t, []string{"CTOKEN"}, gotQuery["token"])
}

func TestLifiClient_TokenDecimalsNotCachedOnError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c := NewLifiClient(srv.URL, "k", srv.Client())
	_, err := c.TokenDecimals(context.Background(), "CTOKEN")
	require.Error(t, err)
	_, err = c.TokenDecimals(context.Background(), "CTOKEN")
	require.Error(t, err)
	assert.EqualValues(t, 2, atomic.LoadInt32(&calls))
}

// A body past maxUpstreamBody is cut off rather than read in full.
func TestLifiClient_OversizedBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"action":{"fromAmount":"1"},"pad":"`))
		_, _ = w.Write(bytes.Repeat([]byte("a"), 3<<20))
		_, _ = w.Write([]byte(`"}`))
	}))
	defer srv.Close()
	_, err := NewLifiClient(srv.URL, "k", srv.Client()).Quote(context.Background(), "a", "b", "1", "c", "0.005")
	require.Error(t, err)
	var ue *metrics.UpstreamError
	assert.ErrorAs(t, err, &ue)
}

// The quote URL carries fromAddress; transport errors must not echo it, since
// the router logs source errors.
func TestLifiClient_TransportErrorOmitsAccount(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()
	_, err := NewLifiClient(srv.URL, "k", srv.Client()).Quote(context.Background(), "a", "b", "1", "GADDRMUSTNOTLEAK", "0.005")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "GADDRMUSTNOTLEAK")
	var ue *metrics.UpstreamError
	assert.ErrorAs(t, err, &ue)
}

func TestLifiClient_DoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()

	_, err := NewLifiClient(srv.URL, "test-key", srv.Client()).Quote(context.Background(), "a", "b", "1", "c", "0.005")
	require.Error(t, err)
	assert.Zero(t, hits.Load())
}
