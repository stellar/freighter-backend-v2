package services

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

func newTestStellarExpert(t *testing.T, handler http.Handler) (types.StellarExpertService, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	svc := NewStellarExpertService(server.URL+"/explorer/public", server.URL+"/explorer/testnet", "test-key", "", nil)
	return svc, server
}

func TestStellarExpert_GetAsset_Success(t *testing.T) {
	t.Parallel()

	var gotPath, gotAuth, gotOrigin string
	body := `{"price":0.15968,"price7d":[[1776902400,0.1755],[1777507200,0.1597]]}`
	svc, _ := newTestStellarExpert(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotOrigin = r.Header.Get("Origin")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))

	asset, err := svc.GetAsset(context.Background(), types.PUBLIC, "XLM")
	require.NoError(t, err)
	assert.Equal(t, "/explorer/public/asset/XLM", gotPath)
	assert.Equal(t, "Bearer test-key", gotAuth)
	assert.Equal(t, "https://stellar.expert", gotOrigin)
	assert.InDelta(t, 0.15968, asset.Price, 1e-9)
	require.Len(t, asset.Price7d, 2)
	assert.InDelta(t, 0.1597, asset.Price7d[1][1], 1e-9)
}

func TestStellarExpert_GetAsset_OmitsAuthHeaderWhenKeyEmpty(t *testing.T) {
	t.Parallel()

	var gotAuthHeaderPresent bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, gotAuthHeaderPresent = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{"price":1,"price7d":[]}`))
	}))
	t.Cleanup(server.Close)

	svc := NewStellarExpertService(server.URL+"/explorer/public", "", "", "", nil)
	_, err := svc.GetAsset(context.Background(), types.PUBLIC, "XLM")
	require.NoError(t, err)
	assert.False(t, gotAuthHeaderPresent, "expected no Authorization header when apiKey is empty")
}

func TestStellarExpert_GetAsset_UsesConfiguredOrigin(t *testing.T) {
	t.Parallel()

	var gotOrigin string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrigin = r.Header.Get("Origin")
		_, _ = w.Write([]byte(`{"price":1,"price7d":[]}`))
	}))
	t.Cleanup(server.Close)

	svc := NewStellarExpertService(server.URL+"/explorer/public", "", "test-key", "https://api.freighter.app", nil)
	_, err := svc.GetAsset(context.Background(), types.PUBLIC, "XLM")
	require.NoError(t, err)
	assert.Equal(t, "https://api.freighter.app", gotOrigin)
}

func TestStellarExpert_GetAsset_TestnetURL(t *testing.T) {
	t.Parallel()

	var gotPath string
	svc, _ := newTestStellarExpert(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"price":1,"price7d":[]}`))
	}))

	_, err := svc.GetAsset(context.Background(), types.TESTNET, "USDC-GA5Z-1")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(gotPath, "/explorer/testnet/asset/"), "got %q", gotPath)
}

func TestStellarExpert_GetAsset_NotFound(t *testing.T) {
	t.Parallel()

	svc, _ := newTestStellarExpert(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"status":404,"error":"not found"}`, http.StatusNotFound)
	}))

	_, err := svc.GetAsset(context.Background(), types.PUBLIC, "BOGUS-G...-1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAssetNotFound))
}

func TestStellarExpert_GetAsset_Malformed(t *testing.T) {
	t.Parallel()

	svc, _ := newTestStellarExpert(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `bad`, http.StatusBadRequest)
	}))

	_, err := svc.GetAsset(context.Background(), types.PUBLIC, "garbled")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAssetMalformed))
}

func TestStellarExpert_GetAsset_ServerError(t *testing.T) {
	t.Parallel()

	svc, _ := newTestStellarExpert(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))

	_, err := svc.GetAsset(context.Background(), types.PUBLIC, "XLM")
	require.Error(t, err)
	var upstream *metrics.UpstreamError
	require.True(t, errors.As(err, &upstream))
	assert.Equal(t, http.StatusBadGateway, upstream.Code)
}

func TestStellarExpert_GetAsset_NetworkNotConfigured(t *testing.T) {
	t.Parallel()

	svc := NewStellarExpertService("https://example.invalid", "", "test-key", "", nil)
	_, err := svc.GetAsset(context.Background(), types.TESTNET, "XLM")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNetworkNotConfigured))
}

func TestStellarExpert_GetAsset_RejectsUnknownNetwork(t *testing.T) {
	t.Parallel()

	svc := NewStellarExpertService("https://a", "https://b", "test-key", "", nil)
	_, err := svc.GetAsset(context.Background(), types.FUTURENET, "XLM")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNetworkNotConfigured))
}

func TestStellarExpert_GetAssetCandles_Success(t *testing.T) {
	t.Parallel()

	var gotPath, gotQuery, gotOrigin string
	body := `[
		[1739707200, 0.001, 0.0009, 0.0011, 0.00105, 1, 1, 5],
		[1739710800, 0.00105, 0.001, 0.00108, 0.00106, 2, 2, 6]
	]`
	svc, _ := newTestStellarExpert(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotOrigin = r.Header.Get("Origin")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))

	from := time.Unix(1739707200, 0).UTC()
	to := time.Unix(1739710800, 0).UTC()
	candles, err := svc.GetAssetCandles(context.Background(), types.PUBLIC, "USDC-G..-1", from, to, 3600)
	require.NoError(t, err)
	require.Len(t, candles, 2)

	assert.Equal(t, "/explorer/public/asset/USDC-G..-1/candles", gotPath)
	assert.Contains(t, gotQuery, "from=1739707200")
	assert.Contains(t, gotQuery, "to=1739710800")
	assert.Contains(t, gotQuery, "resolution=3600")
	assert.Contains(t, gotQuery, "order=asc")
	assert.Equal(t, "https://stellar.expert", gotOrigin)
	assert.InDelta(t, 0.001, candles[0].Open(), 1e-9)
	assert.InDelta(t, 0.00106, candles[1].Close(), 1e-9)
	assert.Equal(t, int64(1739707200), candles[0].TS())
}

func TestStellarExpert_GetAssetCandles_EmptyForNativeXLM(t *testing.T) {
	t.Parallel()

	svc, _ := newTestStellarExpert(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))

	candles, err := svc.GetAssetCandles(context.Background(), types.PUBLIC, "XLM", time.Now().Add(-time.Hour), time.Now(), 3600)
	require.NoError(t, err)
	assert.Empty(t, candles)
}

func TestStellarExpert_GetAssetCandles_NotFound(t *testing.T) {
	t.Parallel()

	svc, _ := newTestStellarExpert(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"status":404}`, http.StatusNotFound)
	}))

	_, err := svc.GetAssetCandles(context.Background(), types.PUBLIC, "BOGUS", time.Now().Add(-time.Hour), time.Now(), 3600)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAssetNotFound))
}

func TestStellarExpert_GetAssetCandles_ServerError(t *testing.T) {
	t.Parallel()

	svc, _ := newTestStellarExpert(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))

	_, err := svc.GetAssetCandles(context.Background(), types.PUBLIC, "XLM", time.Now().Add(-time.Hour), time.Now(), 3600)
	require.Error(t, err)
	var upstream *metrics.UpstreamError
	require.True(t, errors.As(err, &upstream))
	assert.Equal(t, http.StatusBadGateway, upstream.Code)
}

func TestStellarExpert_Name(t *testing.T) {
	t.Parallel()
	svc := NewStellarExpertService("a", "b", "test-key", "", nil)
	assert.Equal(t, "stellar-expert", svc.Name())
}
