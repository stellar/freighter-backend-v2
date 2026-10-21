package swap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
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
