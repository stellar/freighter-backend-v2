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

func strPtr(s string) *string { return &s }
func i64Ptr(v int64) *int64   { return &v }

func TestTokenStatsHandler_Success(t *testing.T) {
	t.Parallel()

	mock := &utils.MockTokenStatsService{
		GetTokenStatsOverride: &types.TokenStats{
			SupplyOnStellar: strPtr("105443902087.3472865"),
			Holders:         i64Ptr(9926520),
		},
	}
	handler := NewTokenStatsHandler(mock)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/token-stats?token=native&network=PUBLIC", nil)
	rr := httptest.NewRecorder()

	require.NoError(t, handler.GetTokenStats(rr, req))
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Data struct {
			Token           string  `json:"token"`
			SupplyOnStellar *string `json:"supplyOnStellar"`
			Holders         *int64  `json:"holders"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, "native", resp.Data.Token, "echoes the client's original token string")
	assert.Equal(t, "XLM", mock.LastCanonical)
	assert.Equal(t, "PUBLIC", mock.LastNetwork)
	require.NotNil(t, resp.Data.SupplyOnStellar)
	assert.Equal(t, "105443902087.3472865", *resp.Data.SupplyOnStellar)
	require.NotNil(t, resp.Data.Holders)
	assert.Equal(t, int64(9926520), *resp.Data.Holders)
}

// D4 on the wire: absent rows are omitted keys, never nulls or zeros.
func TestTokenStatsHandler_OmitsAbsentFields(t *testing.T) {
	t.Parallel()

	mock := &utils.MockTokenStatsService{GetTokenStatsOverride: &types.TokenStats{}}
	handler := NewTokenStatsHandler(mock)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/token-stats?token=native&network=PUBLIC", nil)
	rr := httptest.NewRecorder()

	require.NoError(t, handler.GetTokenStats(rr, req))
	assert.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, `"token":"native"`)
	assert.NotContains(t, body, "supplyOnStellar")
	assert.NotContains(t, body, "holders")
	assert.NotContains(t, body, "null")
}

func TestTokenStatsHandler_BadRequests(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		url  string
	}{
		{"missing network", "/api/v1/token-stats?token=native"},
		{"unknown network", "/api/v1/token-stats?token=native&network=MAINNET"},
		{"futurenet rejected", "/api/v1/token-stats?token=native&network=FUTURENET"},
		{"missing token", "/api/v1/token-stats?network=PUBLIC"},
		{"malformed token", "/api/v1/token-stats?token=bad-format&network=PUBLIC"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mock := &utils.MockTokenStatsService{}
			handler := NewTokenStatsHandler(mock)
			req, _ := http.NewRequest(http.MethodGet, tc.url, nil)
			rr := httptest.NewRecorder()

			err := handler.GetTokenStats(rr, req)
			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, unwrapHttpStatus(t, err))
			assert.Empty(t, mock.LastNetwork, "invalid params must never reach the service")
		})
	}
}

func TestTokenStatsHandler_ErrorMapping(t *testing.T) {
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
			handler := NewTokenStatsHandler(&utils.MockTokenStatsService{GetTokenStatsError: tc.err})
			req, _ := http.NewRequest(http.MethodGet, "/api/v1/token-stats?token=native&network=PUBLIC", nil)
			rr := httptest.NewRecorder()

			err := handler.GetTokenStats(rr, req)
			require.Error(t, err)
			assert.Equal(t, tc.wantCode, unwrapHttpStatus(t, err))
		})
	}
}
