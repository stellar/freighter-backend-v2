// ABOUTME: Unit tests for the account-history handlers and shared translation helpers.
// ABOUTME: Drives handlers with MockWalletBackendService to assert HTTP status, body shape, and error mapping.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/wallet-backend/pkg/wbclient"
	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const testAddress = "GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"

func TestTranslateServiceError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"account not found -> 404", wbclient.ErrAccountNotFound, http.StatusNotFound},
		{"ctx deadline -> 504", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"ctx canceled -> 504", context.Canceled, http.StatusGatewayTimeout},
		{"graphql_error -> 502", &metrics.UpstreamError{Kind: "graphql_error", Err: errors.New("schema bug")}, http.StatusBadGateway},
		{"http_error -> 502", &metrics.UpstreamError{Kind: "http_error", Code: 503, Err: errors.New("upstream down")}, http.StatusBadGateway},
		{"url.Error -> 502", &url.Error{Op: "Post", URL: "http://wb/graphql", Err: errors.New("dial tcp: connection refused")}, http.StatusBadGateway},
		{"net.OpError -> 502", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, http.StatusBadGateway},
		{"generic -> 500", errors.New("anything else"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := translateServiceError(ctx, tt.err, "test resource", testAddress, "PUBLIC")
			require.NotNil(t, h)
			assert.Equal(t, tt.wantStatus, h.StatusCode)
		})
	}
}

func TestNewAccountHistoryHandler_Validation(t *testing.T) {
	t.Parallel()
	mockSvc := &utils.MockWalletBackendService{}
	cases := []struct {
		name     string
		def, max int
		wantErr  bool
	}{
		{"valid", 20, 100, false},
		{"defaultLimit zero", 0, 100, true},
		{"defaultLimit negative", -1, 100, true},
		{"maxLimit zero", 20, 0, true},
		{"maxLimit negative", 20, -1, true},
		{"default greater than max", 200, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, err := NewAccountHistoryHandler(mockSvc, tc.def, tc.max)
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, h)
			} else {
				require.NoError(t, err)
				require.NotNil(t, h)
			}
		})
	}
}

func TestParseRequest(t *testing.T) {
	t.Parallel()
	mockSvc := &utils.MockWalletBackendService{}
	h, err := NewAccountHistoryHandler(mockSvc, 20, 100)
	require.NoError(t, err)

	t.Run("happy path defaults", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		addr, network, p, herr := h.parseRequest(req)
		require.Nil(t, herr)
		assert.Equal(t, testAddress, addr)
		assert.Equal(t, "PUBLIC", network)
		assert.EqualValues(t, 20, p.Limit) // default
		assert.Equal(t, types.PaginationDirectionNext, p.Direction)
		assert.Nil(t, p.Cursor)
		assert.Nil(t, p.Since)
		assert.Nil(t, p.Until)
	})

	t.Run("all params parsed", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/x?network=TESTNET&limit=10&cursor=abc&direction=prev&since=2026-01-01T00:00:00Z&until=2026-02-01T00:00:00Z", nil)
		req.SetPathValue("address", testAddress)
		addr, network, p, herr := h.parseRequest(req)
		require.Nil(t, herr)
		assert.Equal(t, testAddress, addr)
		assert.Equal(t, "TESTNET", network)
		assert.EqualValues(t, 10, p.Limit)
		assert.Equal(t, types.PaginationDirectionPrev, p.Direction)
		require.NotNil(t, p.Cursor)
		assert.Equal(t, "abc", *p.Cursor)
		require.NotNil(t, p.Since)
		require.NotNil(t, p.Until)
		assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), *p.Since)
		assert.Equal(t, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), *p.Until)
	})

	rejection := []struct {
		name, query, address string
	}{
		{"invalid address", "network=PUBLIC", "not-a-stellar-address"},
		{"missing network", "", testAddress},
		{"invalid network futurenet", "network=FUTURENET", testAddress},
		{"invalid network garbage", "network=foo", testAddress},
		{"limit zero", "network=PUBLIC&limit=0", testAddress},
		{"limit too big", "network=PUBLIC&limit=101", testAddress},
		{"limit non-integer", "network=PUBLIC&limit=abc", testAddress},
		{"direction garbage", "network=PUBLIC&direction=sideways", testAddress},
		{"since garbage", "network=PUBLIC&since=not-a-time", testAddress},
		{"until garbage", "network=PUBLIC&until=not-a-time", testAddress},
		{"since after until", "network=PUBLIC&since=2026-02-01T00:00:00Z&until=2026-01-01T00:00:00Z", testAddress},
	}
	for _, tc := range rejection {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/x?"+tc.query, nil)
			req.SetPathValue("address", tc.address)
			_, _, _, herr := h.parseRequest(req)
			require.NotNil(t, herr)
			assert.Equal(t, http.StatusBadRequest, herr.StatusCode)
		})
	}
}

func TestGetAccountTransactions_Handler(t *testing.T) {
	t.Parallel()

	t.Run("happy path 200 with paginated body", func(t *testing.T) {
		t.Parallel()
		nextCursor := "n"
		mockSvc := &utils.MockWalletBackendService{
			GetAccountTransactionsResult: &types.PaginatedResponse[*wbtypes.GraphQLTransaction]{
				Data: []*wbtypes.GraphQLTransaction{{Hash: "h1"}},
				Pagination: types.PaginationInfo{
					NextCursor:  &nextCursor,
					HasNext:     true,
					HasPrevious: false,
				},
			},
		}
		h, err := NewAccountHistoryHandler(mockSvc, 20, 100)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		require.NoError(t, h.GetAccountTransactions(rr, req))
		assert.Equal(t, http.StatusOK, rr.Code)

		var body types.PaginatedResponse[*wbtypes.GraphQLTransaction]
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
		require.Len(t, body.Data, 1)
		assert.Equal(t, "h1", body.Data[0].Hash)
		assert.True(t, body.Pagination.HasNext)
	})

	t.Run("404 when service returns ErrAccountNotFound", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{GetAccountTransactionsError: wbclient.ErrAccountNotFound}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountTransactions(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusNotFound, herr.StatusCode)
	})

	t.Run("502 when service returns graphql_error", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{GetAccountTransactionsError: &metrics.UpstreamError{Kind: "graphql_error", Err: errors.New("schema bug")}}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountTransactions(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusBadGateway, herr.StatusCode)
	})

	t.Run("502 when service returns http_error", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{GetAccountTransactionsError: &metrics.UpstreamError{Kind: "http_error", Code: 503, Err: errors.New("upstream down")}}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountTransactions(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusBadGateway, herr.StatusCode)
	})

	t.Run("504 when service returns context.DeadlineExceeded", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{GetAccountTransactionsError: context.DeadlineExceeded}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountTransactions(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusGatewayTimeout, herr.StatusCode)
	})

	t.Run("500 when service returns generic error", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{GetAccountTransactionsError: errors.New("boom")}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountTransactions(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusInternalServerError, herr.StatusCode)
	})

	t.Run("400 on invalid request (validation forwarded)", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=FUTURENET", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountTransactions(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusBadRequest, herr.StatusCode)
	})

	t.Run("forwards parsed params to the service", func(t *testing.T) {
		t.Parallel()
		var (
			gotAddress string
			gotNetwork string
			gotParams  types.AccountHistoryParams
		)
		mockSvc := &utils.MockWalletBackendService{
			GetAccountTransactionsFunc: func(_ context.Context, addr, network string, p types.AccountHistoryParams) (*types.PaginatedResponse[*wbtypes.GraphQLTransaction], error) {
				gotAddress = addr
				gotNetwork = network
				gotParams = p
				return &types.PaginatedResponse[*wbtypes.GraphQLTransaction]{Data: []*wbtypes.GraphQLTransaction{}, Pagination: types.PaginationInfo{}}, nil
			},
		}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)

		req := httptest.NewRequest(http.MethodGet, "/x?network=TESTNET&limit=5&cursor=abc&direction=prev&since=2026-01-01T00:00:00Z&until=2026-02-01T00:00:00Z", nil)
		req.SetPathValue("address", testAddress)
		require.NoError(t, h.GetAccountTransactions(httptest.NewRecorder(), req))

		assert.Equal(t, testAddress, gotAddress)
		assert.Equal(t, "TESTNET", gotNetwork)
		assert.EqualValues(t, 5, gotParams.Limit)
		assert.Equal(t, types.PaginationDirectionPrev, gotParams.Direction)
		require.NotNil(t, gotParams.Cursor)
		assert.Equal(t, "abc", *gotParams.Cursor)
		require.NotNil(t, gotParams.Since)
		require.NotNil(t, gotParams.Until)
	})

	t.Run("empty data slice marshals as []", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{
			GetAccountTransactionsResult: &types.PaginatedResponse[*wbtypes.GraphQLTransaction]{
				Data:       []*wbtypes.GraphQLTransaction{},
				Pagination: types.PaginationInfo{HasNext: false, HasPrevious: false},
			},
		}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		require.NoError(t, h.GetAccountTransactions(rr, req))
		assert.Contains(t, rr.Body.String(), `"data":[]`)
	})
}

func TestGetAccountOperations_Handler(t *testing.T) {
	t.Parallel()

	t.Run("happy path 200 with paginated body", func(t *testing.T) {
		t.Parallel()
		nextCursor := "n"
		mockSvc := &utils.MockWalletBackendService{
			GetAccountOperationsResult: &types.PaginatedResponse[*wbtypes.Operation]{
				Data: []*wbtypes.Operation{{ID: 1, OperationType: wbtypes.OperationTypePayment}},
				Pagination: types.PaginationInfo{
					NextCursor:  &nextCursor,
					HasNext:     true,
					HasPrevious: false,
				},
			},
		}
		h, err := NewAccountHistoryHandler(mockSvc, 20, 100)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		require.NoError(t, h.GetAccountOperations(rr, req))
		assert.Equal(t, http.StatusOK, rr.Code)

		var body types.PaginatedResponse[*wbtypes.Operation]
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
		require.Len(t, body.Data, 1)
		assert.Equal(t, int64(1), body.Data[0].ID)
		assert.True(t, body.Pagination.HasNext)
	})

	t.Run("404 when service returns ErrAccountNotFound", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{GetAccountOperationsError: wbclient.ErrAccountNotFound}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountOperations(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusNotFound, herr.StatusCode)
	})

	t.Run("502 when service returns graphql_error", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{GetAccountOperationsError: &metrics.UpstreamError{Kind: "graphql_error", Err: errors.New("schema bug")}}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountOperations(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusBadGateway, herr.StatusCode)
	})

	t.Run("502 when service returns http_error", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{GetAccountOperationsError: &metrics.UpstreamError{Kind: "http_error", Code: 503, Err: errors.New("upstream down")}}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountOperations(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusBadGateway, herr.StatusCode)
	})

	t.Run("504 when service returns context.DeadlineExceeded", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{GetAccountOperationsError: context.DeadlineExceeded}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountOperations(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusGatewayTimeout, herr.StatusCode)
	})

	t.Run("500 when service returns generic error", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{GetAccountOperationsError: errors.New("boom")}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountOperations(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusInternalServerError, herr.StatusCode)
	})

	t.Run("400 on invalid request (validation forwarded)", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/x?network=FUTURENET", nil)
		req.SetPathValue("address", testAddress)
		err := h.GetAccountOperations(httptest.NewRecorder(), req)
		var herr *httperror.HttpError
		require.True(t, errors.As(err, &herr))
		assert.Equal(t, http.StatusBadRequest, herr.StatusCode)
	})

	t.Run("forwards parsed params to the service", func(t *testing.T) {
		t.Parallel()
		var (
			gotAddress string
			gotNetwork string
			gotParams  types.AccountHistoryParams
		)
		mockSvc := &utils.MockWalletBackendService{
			GetAccountOperationsFunc: func(_ context.Context, addr, network string, p types.AccountHistoryParams) (*types.PaginatedResponse[*wbtypes.Operation], error) {
				gotAddress = addr
				gotNetwork = network
				gotParams = p
				return &types.PaginatedResponse[*wbtypes.Operation]{Data: []*wbtypes.Operation{}, Pagination: types.PaginationInfo{}}, nil
			},
		}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)

		req := httptest.NewRequest(http.MethodGet, "/x?network=TESTNET&limit=5&cursor=abc&direction=prev&since=2026-01-01T00:00:00Z&until=2026-02-01T00:00:00Z", nil)
		req.SetPathValue("address", testAddress)
		require.NoError(t, h.GetAccountOperations(httptest.NewRecorder(), req))

		assert.Equal(t, testAddress, gotAddress)
		assert.Equal(t, "TESTNET", gotNetwork)
		assert.EqualValues(t, 5, gotParams.Limit)
		assert.Equal(t, types.PaginationDirectionPrev, gotParams.Direction)
		require.NotNil(t, gotParams.Cursor)
		assert.Equal(t, "abc", *gotParams.Cursor)
		require.NotNil(t, gotParams.Since)
		require.NotNil(t, gotParams.Until)
	})

	t.Run("empty data slice marshals as []", func(t *testing.T) {
		t.Parallel()
		mockSvc := &utils.MockWalletBackendService{
			GetAccountOperationsResult: &types.PaginatedResponse[*wbtypes.Operation]{
				Data:       []*wbtypes.Operation{},
				Pagination: types.PaginationInfo{HasNext: false, HasPrevious: false},
			},
		}
		h, _ := NewAccountHistoryHandler(mockSvc, 20, 100)
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x?network=PUBLIC", nil)
		req.SetPathValue("address", testAddress)
		require.NoError(t, h.GetAccountOperations(rr, req))
		assert.Contains(t, rr.Body.String(), `"data":[]`)
	})
}
