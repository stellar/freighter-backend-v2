package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const validIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

func ptr(s string) *string { return &s }

func TestTokenPrices_Success(t *testing.T) {
	t.Parallel()

	mock := &utils.MockPricesService{
		GetPricesOverride: map[string]*types.PriceEntry{
			"XLM":                 {CurrentPrice: "0.16", PercentagePriceChange24h: ptr("1.27")},
			"USDC:" + validIssuer: {CurrentPrice: "1", PercentagePriceChange24h: ptr("0")},
		},
	}
	handler := NewTokenPricesHandler(mock, 1000)

	body := `{"tokens":["XLM","USDC:` + validIssuer + `"]}`
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/token-prices?network=PUBLIC", strings.NewReader(body))
	rr := httptest.NewRecorder()

	require.NoError(t, handler.GetPrices(rr, req))
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Data map[string]*types.PriceEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Contains(t, resp.Data, "XLM")
	require.Contains(t, resp.Data, "USDC:"+validIssuer)
	assert.Equal(t, "0.16", resp.Data["XLM"].CurrentPrice)
	assert.Equal(t, "1.27", *resp.Data["XLM"].PercentagePriceChange24h)
	assert.Equal(t, "PUBLIC", mock.LastNetwork)
}

func TestTokenPrices_NullableEntry(t *testing.T) {
	t.Parallel()

	mock := &utils.MockPricesService{
		GetPricesOverride: map[string]*types.PriceEntry{
			"BOGUS:" + validIssuer: nil,
		},
	}
	handler := NewTokenPricesHandler(mock, 1000)

	body := `{"tokens":["BOGUS:` + validIssuer + `"]}`
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/token-prices?network=PUBLIC", strings.NewReader(body))
	rr := httptest.NewRecorder()

	require.NoError(t, handler.GetPrices(rr, req))
	assert.Equal(t, http.StatusOK, rr.Code)
	// Verify the wire format: { "data": { "BOGUS:G...": null } }
	assert.Contains(t, rr.Body.String(), `"BOGUS:`+validIssuer+`":null`)
}

func TestTokenPrices_PreservesOriginalInputKeys(t *testing.T) {
	t.Parallel()

	mock := &utils.MockPricesService{
		GetPricesOverride: map[string]*types.PriceEntry{
			"XLM": {CurrentPrice: "0.16", PercentagePriceChange24h: ptr("1.27")},
		},
	}
	handler := NewTokenPricesHandler(mock, 1000)

	// Client sends "native"; response must echo "native", not "XLM".
	body := `{"tokens":["native"]}`
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/token-prices?network=PUBLIC", strings.NewReader(body))
	rr := httptest.NewRecorder()

	require.NoError(t, handler.GetPrices(rr, req))
	var resp struct {
		Data map[string]*types.PriceEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Contains(t, resp.Data, "native")
	assert.NotContains(t, resp.Data, "XLM")
	assert.Equal(t, "0.16", resp.Data["native"].CurrentPrice)
}

func TestTokenPrices_BadRequests(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		url      string
		body     string
		wantCode int
	}{
		{"missing network", "/api/v1/token-prices", `{"tokens":["XLM"]}`, http.StatusBadRequest},
		{"unknown network", "/api/v1/token-prices?network=MAINNET", `{"tokens":["XLM"]}`, http.StatusBadRequest},
		{"futurenet rejected", "/api/v1/token-prices?network=FUTURENET", `{"tokens":["XLM"]}`, http.StatusBadRequest},
		{"empty tokens", "/api/v1/token-prices?network=PUBLIC", `{"tokens":[]}`, http.StatusBadRequest},
		{"invalid JSON", "/api/v1/token-prices?network=PUBLIC", `{garbage`, http.StatusBadRequest},
		{"malformed token", "/api/v1/token-prices?network=PUBLIC", `{"tokens":["bad-format"]}`, http.StatusBadRequest},
		{"too many unique tokens", "/api/v1/token-prices?network=PUBLIC", `{"tokens":["XLM","USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN","AQUA:GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA"]}`, http.StatusBadRequest},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler := NewTokenPricesHandler(&utils.MockPricesService{}, 2)
			req, _ := http.NewRequest(http.MethodPost, tc.url, strings.NewReader(tc.body))
			rr := httptest.NewRecorder()

			err := handler.GetPrices(rr, req)
			require.Error(t, err)
			// Handler returns the HttpError; in real use the CustomHandler
			// wrapper renders it. Here, assert the embedded status.
			httpErr := unwrapHttpStatus(t, err)
			assert.Equal(t, tc.wantCode, httpErr)
		})
	}
}

// Duplicates collapse to a single canonical id; the cap is enforced on the
// deduped set so a request like ["XLM","native","xlm"] (3 raw, 1 canonical)
// passes a cap of 2.
func TestTokenPrices_DuplicatesCollapseUnderCap(t *testing.T) {
	t.Parallel()

	mock := &utils.MockPricesService{}
	handler := NewTokenPricesHandler(mock, 2)

	body := `{"tokens":["XLM","native","xlm"]}`
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/token-prices?network=PUBLIC", strings.NewReader(body))
	rr := httptest.NewRecorder()

	err := handler.GetPrices(rr, req)
	require.NoError(t, err)
	assert.Equal(t, []string{"XLM"}, mock.LastTokens)
}

func TestTokenPrices_ServiceError(t *testing.T) {
	t.Parallel()

	mock := &utils.MockPricesService{GetPricesError: errors.New("boom")}
	handler := NewTokenPricesHandler(mock, 1000)

	body := `{"tokens":["XLM"]}`
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/token-prices?network=PUBLIC", strings.NewReader(body))
	rr := httptest.NewRecorder()

	err := handler.GetPrices(rr, req)
	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, unwrapHttpStatus(t, err))
}

func TestTokenPrices_ContextErrorReturns503(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"deadline exceeded", context.DeadlineExceeded},
		{"canceled", context.Canceled},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mock := &utils.MockPricesService{GetPricesError: tc.err}
			handler := NewTokenPricesHandler(mock, 1000)

			body := `{"tokens":["XLM"]}`
			req, _ := http.NewRequest(http.MethodPost, "/api/v1/token-prices?network=PUBLIC", strings.NewReader(body))
			rr := httptest.NewRecorder()

			err := handler.GetPrices(rr, req)
			require.Error(t, err)
			assert.Equal(t, http.StatusServiceUnavailable, unwrapHttpStatus(t, err))
		})
	}
}

func unwrapHttpStatus(t *testing.T, err error) int {
	t.Helper()
	type httpStatus interface{ HttpStatus() int }
	var hs httpStatus
	require.True(t, errors.As(err, &hs), "expected HttpError-typed error, got %T", err)
	return hs.HttpStatus()
}
