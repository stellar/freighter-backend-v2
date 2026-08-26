package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func lowVol(v bool) *bool { return &v }

func TestTokenPriceHistory_Success(t *testing.T) {
	t.Parallel()

	mock := &utils.MockPriceHistoryService{
		GetPriceHistoryOverride: &types.TokenPriceHistory{
			Range:             "1D",
			ResolutionSeconds: 900,
			Currency:          "USD",
			LowVolume:         lowVol(false),
			Change:            &types.PriceChange{Absolute: "0.001359", Percent: "0.86"},
			Points:            []types.PricePoint{{T: 1786636800, P: "0.158900"}},
		},
	}
	handler := NewTokenPriceHistoryHandler(mock)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/token-price-history?token=native&network=PUBLIC&range=1D", nil)
	rr := httptest.NewRecorder()

	require.NoError(t, handler.GetTokenPriceHistory(rr, req))
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Data struct {
			Token             string             `json:"token"`
			Range             string             `json:"range"`
			ResolutionSeconds int64              `json:"resolutionSeconds"`
			Currency          string             `json:"currency"`
			LowVolume         *bool              `json:"lowVolume"`
			Change            *types.PriceChange `json:"change"`
			Points            []types.PricePoint `json:"points"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))

	// The response echoes the client's original token string, not the
	// canonical id; the service receives the canonical id.
	assert.Equal(t, "native", resp.Data.Token)
	assert.Equal(t, "XLM", mock.LastCanonical)
	assert.Equal(t, "PUBLIC", mock.LastNetwork)
	assert.Equal(t, "1D", mock.LastRange)

	assert.Equal(t, "1D", resp.Data.Range)
	assert.Equal(t, int64(900), resp.Data.ResolutionSeconds)
	assert.Equal(t, "USD", resp.Data.Currency)
	require.NotNil(t, resp.Data.LowVolume)
	assert.False(t, *resp.Data.LowVolume)
	require.NotNil(t, resp.Data.Change)
	assert.Equal(t, "0.001359", resp.Data.Change.Absolute)
	assert.Equal(t, "0.86", resp.Data.Change.Percent)
	require.Len(t, resp.Data.Points, 1)
	assert.Equal(t, int64(1786636800), resp.Data.Points[0].T)
	assert.Equal(t, "0.158900", resp.Data.Points[0].P)
}

// No data is 200 with points: [] and change: null — never 404 — and
// lowVolume: null serializes as an explicit null, not a missing key.
func TestTokenPriceHistory_EmptySeriesWireShape(t *testing.T) {
	t.Parallel()

	mock := &utils.MockPriceHistoryService{
		GetPriceHistoryOverride: &types.TokenPriceHistory{
			Range:             "1D",
			ResolutionSeconds: 900,
			Currency:          "USD",
			LowVolume:         nil,
			Change:            nil,
			Points:            []types.PricePoint{},
		},
	}
	handler := NewTokenPriceHistoryHandler(mock)

	const contractID = "CBUIULAWELK3QWVO55TUFKTWE7BHC2BOESOIIV3FJKG47GAZWDUMOZFC"
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/token-price-history?token="+contractID+"&network=PUBLIC&range=1D", nil)
	rr := httptest.NewRecorder()

	require.NoError(t, handler.GetTokenPriceHistory(rr, req))
	assert.Equal(t, http.StatusOK, rr.Code)

	body := rr.Body.String()
	assert.Contains(t, body, `"points":[]`, "empty series is [], never null")
	assert.Contains(t, body, `"change":null`)
	assert.Contains(t, body, `"lowVolume":null`, "the null verdict is an explicit null on the wire")
	assert.Contains(t, body, `"token":"`+contractID+`"`)
	assert.Equal(t, contractID, mock.LastCanonical, "bare contract ids pass through as canonical")
}

// The clients' SYMBOL:CONTRACTID form echoes the original key; the service
// sees the bare contract id.
func TestTokenPriceHistory_SymbolContractEcho(t *testing.T) {
	t.Parallel()

	const contractID = "CBIJBDNZNF4X35BJ4FFZWCDBSCKOP5NB4PLG4SNENRMLAPYG4P5FM6VN"
	mock := &utils.MockPriceHistoryService{}
	handler := NewTokenPriceHistoryHandler(mock)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/token-price-history?token=SolvBTC%3A"+contractID+"&network=PUBLIC&range=1W", nil)
	rr := httptest.NewRecorder()

	require.NoError(t, handler.GetTokenPriceHistory(rr, req))
	assert.Contains(t, rr.Body.String(), `"token":"SolvBTC:`+contractID+`"`)
	assert.Equal(t, contractID, mock.LastCanonical)
}

func TestTokenPriceHistory_BadRequests(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		url  string
	}{
		{"missing network", "/api/v1/token-price-history?token=native&range=1D"},
		{"unknown network", "/api/v1/token-price-history?token=native&network=MAINNET&range=1D"},
		{"futurenet rejected", "/api/v1/token-price-history?token=native&network=FUTURENET&range=1D"},
		{"missing token", "/api/v1/token-price-history?network=PUBLIC&range=1D"},
		{"malformed token", "/api/v1/token-price-history?token=bad-format&network=PUBLIC&range=1D"},
		{"missing range", "/api/v1/token-price-history?token=native&network=PUBLIC"},
		{"invalid range", "/api/v1/token-price-history?token=native&network=PUBLIC&range=2D"},
		{"lowercase range rejected", "/api/v1/token-price-history?token=native&network=PUBLIC&range=1d"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mock := &utils.MockPriceHistoryService{}
			handler := NewTokenPriceHistoryHandler(mock)
			req, _ := http.NewRequest(http.MethodGet, tc.url, nil)
			rr := httptest.NewRecorder()

			err := handler.GetTokenPriceHistory(rr, req)
			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, unwrapHttpStatus(t, err))
			assert.Empty(t, mock.LastRange, "invalid params must never reach the service")
		})
	}
}

func TestTokenPriceHistory_ErrorMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{"deadline exceeded → 503", context.DeadlineExceeded, http.StatusServiceUnavailable},
		{"canceled → 503", context.Canceled, http.StatusServiceUnavailable},
		{"other → 500", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler := NewTokenPriceHistoryHandler(&utils.MockPriceHistoryService{GetPriceHistoryError: tc.err})
			req, _ := http.NewRequest(http.MethodGet, "/api/v1/token-price-history?token=native&network=PUBLIC&range=1D", nil)
			rr := httptest.NewRecorder()

			err := handler.GetTokenPriceHistory(rr, req)
			require.Error(t, err)
			assert.Equal(t, tc.wantCode, unwrapHttpStatus(t, err))
		})
	}
}
