package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/services"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const assetSearchBody = `{"_links":{"self":{"href":"/explorer/public/asset?search=usdc&order=desc&limit=50&cursor=0"}},"_embedded":{"records":[{"asset":"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN-1","domain":"centre.io","price":1.0001}]}}`

func TestAssetSearchHandler_ServesStellarExpertBodyUnderData(t *testing.T) {
	t.Parallel()

	mock := &utils.MockAssetSearchService{Body: json.RawMessage(assetSearchBody)}
	handler := NewAssetSearchHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/assets/search?network=PUBLIC&search=usdc&limit=50", nil)
	rr := httptest.NewRecorder()

	require.NoError(t, handler.SearchAssets(rr, req))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var resp struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.JSONEq(t, assetSearchBody, string(resp.Data), "data is StellarExpert's body, unchanged")

	assert.Equal(t, types.PUBLIC, mock.LastNetwork)
	assert.Equal(t, url.Values{"search": {"usdc"}, "limit": {"50"}}, mock.LastParams,
		"network is ours, not StellarExpert's: it must not be forwarded")
}

func TestAssetSearchHandler_RejectsBadNetworkBeforeTheService(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, url string }{
		{"missing network", "/api/v1/assets/search?search=usdc"},
		{"unknown network", "/api/v1/assets/search?network=MAINNET&search=usdc"},
		{"lowercase network", "/api/v1/assets/search?network=public&search=usdc"},
		{"futurenet", "/api/v1/assets/search?network=FUTURENET&search=usdc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mock := &utils.MockAssetSearchService{Body: json.RawMessage(assetSearchBody)}
			handler := NewAssetSearchHandler(mock)

			err := handler.SearchAssets(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.url, nil))
			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, unwrapHttpStatus(t, err))
			assert.Zero(t, mock.Calls, "a bad network must never reach the service")
		})
	}
}

// Clients fall back to held tokens on any non-2xx, so these codes are for us:
// 400 = the caller's query, 500 = our credentials (loud: it fails every
// request until config changes), 503 = an upstream blip that self-heals.
func TestAssetSearchHandler_ErrorMapping(t *testing.T) {
	t.Parallel()

	upstream := func(code int, sentinel error) error {
		inner := fmt.Errorf("stellar expert asset list status %d", code)
		if sentinel != nil {
			inner = fmt.Errorf("%w: stellar expert asset list status %d", sentinel, code)
		}
		return &metrics.UpstreamError{Kind: "http_error", Code: code, Err: inner}
	}
	for _, tc := range []struct {
		name     string
		err      error
		wantCode int
	}{
		{"refused params → 400", fmt.Errorf("%w: unsupported parameter \"q\"", services.ErrInvalidAssetSearchParams), http.StatusBadRequest},
		{"StellarExpert rejects the query (400) → 400", upstream(http.StatusBadRequest, services.ErrAssetMalformed), http.StatusBadRequest},
		{"key rejected or balance spent (402) → 500", upstream(http.StatusPaymentRequired, services.ErrUpstreamAuth), http.StatusInternalServerError},
		{"key rejected (401) → 500", upstream(http.StatusUnauthorized, services.ErrUpstreamAuth), http.StatusInternalServerError},
		{"rate limited (429) → 503", upstream(http.StatusTooManyRequests, nil), http.StatusServiceUnavailable},
		{"upstream 5xx → 503", upstream(http.StatusBadGateway, nil), http.StatusServiceUnavailable},
		{"transport failure → 503", &metrics.UpstreamError{Kind: "http_error", Err: errors.New("connection reset")}, http.StatusServiceUnavailable},
		{"deadline exceeded → 503", context.DeadlineExceeded, http.StatusServiceUnavailable},
		{"canceled → 503", context.Canceled, http.StatusServiceUnavailable},
		{"anything else is ours → 500", errors.New("decoding stellar expert asset list response: boom"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler := NewAssetSearchHandler(&utils.MockAssetSearchService{Err: tc.err})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/assets/search?network=PUBLIC&search=usdc", nil)

			err := handler.SearchAssets(httptest.NewRecorder(), req)
			require.Error(t, err)
			assert.Equal(t, tc.wantCode, unwrapHttpStatus(t, err))
		})
	}
}

// A refused query must say why, or a client developer cannot fix it.
func TestAssetSearchHandler_RefusedParamsExplainWhy(t *testing.T) {
	t.Parallel()

	reason := fmt.Errorf("%w: unsupported parameter \"q\"", services.ErrInvalidAssetSearchParams)
	handler := NewAssetSearchHandler(&utils.MockAssetSearchService{Err: reason})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/assets/search?network=PUBLIC&q=usdc", nil)

	err := handler.SearchAssets(httptest.NewRecorder(), req)
	var httpErr *httperror.HttpError
	require.ErrorAs(t, err, &httpErr)
	assert.Contains(t, httpErr.Message, `unsupported parameter "q"`)
}
