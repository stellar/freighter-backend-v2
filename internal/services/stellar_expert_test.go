package services

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

func newTestStellarExpert(t *testing.T, handler http.Handler) (types.StellarExpertService, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	svc := NewStellarExpertService(server.URL+"/explorer/public", server.URL+"/explorer/testnet", nil)
	return svc, server
}

func TestStellarExpert_GetAsset_Success(t *testing.T) {
	t.Parallel()

	var gotPath string
	body := `{"price":0.15968,"price7d":[[1776902400,0.1755],[1777507200,0.1597]]}`
	svc, _ := newTestStellarExpert(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))

	asset, err := svc.GetAsset(context.Background(), types.PUBLIC, "XLM")
	require.NoError(t, err)
	assert.Equal(t, "/explorer/public/asset/XLM", gotPath)
	assert.InDelta(t, 0.15968, asset.Price, 1e-9)
	require.Len(t, asset.Price7d, 2)
	assert.InDelta(t, 0.1597, asset.Price7d[1][1], 1e-9)
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

	svc := NewStellarExpertService("https://example.invalid", "", nil)
	_, err := svc.GetAsset(context.Background(), types.TESTNET, "XLM")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNetworkNotConfigured))
}

func TestStellarExpert_GetAsset_RejectsUnknownNetwork(t *testing.T) {
	t.Parallel()

	svc := NewStellarExpertService("https://a", "https://b", nil)
	_, err := svc.GetAsset(context.Background(), types.FUTURENET, "XLM")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNetworkNotConfigured))
}

func TestStellarExpert_Name(t *testing.T) {
	t.Parallel()
	svc := NewStellarExpertService("a", "b", nil)
	assert.Equal(t, "stellar-expert", svc.Name())
}
