// ABOUTME: Tests for the Cross Mesh Ingress API client against an httptest server.
// ABOUTME: Covers request shape, response decoding, the error envelope, timeouts, and that the API key never leaks.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	testCrossMeshKey       = "sk_test_secret_value"
	testCrossMeshRecipient = "GBRAD6AQVETCVZDBT3TLU3KXGPQIXOIQX5DLJYKHNZ4ZF6AWYQXEZGQD"
)

// newTestCrossMesh starts a server running handler and returns a client whose
// testnet environment points at it.
func newTestCrossMesh(t *testing.T, handler http.HandlerFunc) *crossMeshService {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	svc, ok := NewCrossMeshService(config.CrossMeshConfig{TestnetURL: srv.URL, TestnetAPIKey: testCrossMeshKey}, nil).(*crossMeshService)
	require.True(t, ok)
	return svc
}

// serveJSON answers every request with status and body, after letting check
// inspect the request.
func serveJSON(t *testing.T, status int, body string, check func(*http.Request)) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+testCrossMeshKey, r.Header.Get("Authorization"))
		if check != nil {
			check(r)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestCrossMesh_GetChains(t *testing.T) {
	t.Parallel()
	body := `{"contracts":null,"xlm_delivery":null,"chains":[
		{"chain":"polygon","chain_id":80002,"status":"active","usdc_address":"0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582",
		 "confirmations":3,"min_settlement_amount":"1000000","decimals":6,
		 "speeds":{"standard":{"available":true,"max_cctp_fee_ppm":"0","estimated_delivery_seconds":1200},
		           "fast":{"available":false,"max_cctp_fee_ppm":null,"estimated_delivery_seconds":null}},
		 "fees":{"setup_fee":"500000","base_fee":"100000","fee_rate_ppm":"0","decimals":6}},
		{"chain":"ethereum","chain_id":11155111,"status":"suspended","usdc_address":"0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
		 "confirmations":12,"min_settlement_amount":"2000000","decimals":6,
		 "speeds":{"standard":{"available":false,"max_cctp_fee_ppm":null,"estimated_delivery_seconds":null},
		           "fast":{"available":false,"max_cctp_fee_ppm":null,"estimated_delivery_seconds":null}},
		 "fees":null}]}`
	svc := newTestCrossMesh(t, serveJSON(t, http.StatusOK, body, func(r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/chains", r.URL.Path)
	}))

	got, err := svc.GetChains(context.Background(), types.TESTNET)
	require.NoError(t, err)
	require.Len(t, got.Chains, 2)

	polygon := got.Chains[0]
	assert.Equal(t, "polygon", polygon.Chain)
	assert.Equal(t, int64(80002), polygon.ChainID)
	assert.Equal(t, "active", polygon.Status)
	assert.Equal(t, "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582", polygon.USDCAddress)
	assert.Equal(t, int64(3), polygon.Confirmations)
	assert.Equal(t, "1000000", polygon.MinSettlementAmount)
	assert.Equal(t, 6, polygon.Decimals)
	require.NotNil(t, polygon.Speeds.Standard.EstimatedDeliverySeconds)
	assert.Equal(t, int64(1200), *polygon.Speeds.Standard.EstimatedDeliverySeconds)
	require.NotNil(t, polygon.Fees)
	assert.Equal(t, types.CrossMeshChainFees{SetupFee: "500000", BaseFee: "100000", FeeRatePPM: "0", Decimals: 6}, *polygon.Fees)

	assert.Nil(t, got.Chains[1].Speeds.Standard.EstimatedDeliverySeconds)
	assert.Nil(t, got.Chains[1].Fees)
}

const testDepositAddressRow = `{"deposit_address":"0x93845A25A3a5F9b15c25532797CE56fD4851Fcce",
	"recipient":"GBRAD6AQVETCVZDBT3TLU3KXGPQIXOIQX5DLJYKHNZ4ZF6AWYQXEZGQD","delivery_asset":"USDC",
	"speed":"standard","index":0,"created_at":"2026-11-13T00:00:00Z"`

func TestCrossMesh_CreateDepositAddress(t *testing.T) {
	t.Parallel()
	svc := newTestCrossMesh(t, serveJSON(t, http.StatusOK, testDepositAddressRow+"}", func(r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/deposit-addresses", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var sent map[string]string
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&sent))
		assert.Equal(t, map[string]string{"recipient": testCrossMeshRecipient, "delivery_asset": "USDC", "speed": "standard"}, sent)
	}))

	got, err := svc.CreateDepositAddress(context.Background(), types.TESTNET, testCrossMeshRecipient)
	require.NoError(t, err)
	assert.Equal(t, types.CrossMeshDepositAddress{
		DepositAddress: "0x93845A25A3a5F9b15c25532797CE56fD4851Fcce",
		Recipient:      testCrossMeshRecipient,
		DeliveryAsset:  "USDC",
		Speed:          "standard",
		Index:          0,
		CreatedAt:      "2026-11-13T00:00:00Z",
	}, *got)
}

func TestCrossMesh_LookupDepositAddress(t *testing.T) {
	t.Parallel()
	checkQuery := func(r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/deposit-addresses", r.URL.Path)
		q := r.URL.Query()
		assert.Equal(t, testCrossMeshRecipient, q.Get("recipient"))
		assert.Equal(t, "USDC", q.Get("delivery_asset"))
		assert.Equal(t, "standard", q.Get("speed"))
	}

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		body := `{"data":[` + testDepositAddressRow + `,"last_deposit_at":null}],"has_more":false,"next_cursor":null}`
		svc := newTestCrossMesh(t, serveJSON(t, http.StatusOK, body, checkQuery))
		got, err := svc.LookupDepositAddress(context.Background(), types.TESTNET, testCrossMeshRecipient)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "0x93845A25A3a5F9b15c25532797CE56fD4851Fcce", got.DepositAddress)
	})

	t.Run("none yet", func(t *testing.T) {
		t.Parallel()
		svc := newTestCrossMesh(t, serveJSON(t, http.StatusOK, `{"data":[],"has_more":false,"next_cursor":null}`, checkQuery))
		got, err := svc.LookupDepositAddress(context.Background(), types.TESTNET, testCrossMeshRecipient)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestCrossMesh_ListDeposits(t *testing.T) {
	t.Parallel()
	body := `{"data":[
		{"deposit_id":"dep_2","deposit_address":"0x93845A25A3a5F9b15c25532797CE56fD4851Fcce","status":"held",
		 "hold_reason":"below_minimum","hold_detail":"balance below minimum","settlement_id":"stl_2","amount":"500000",
		 "decimals":6,"recipient":"GBRAD6AQVETCVZDBT3TLU3KXGPQIXOIQX5DLJYKHNZ4ZF6AWYQXEZGQD","delivery_asset":"USDC",
		 "source_chain":"polygon","source_chain_id":80002,"source_tx":"0xabc","destination_tx":null,
		 "created_at":"2026-11-13T01:00:00Z","updated_at":"2026-11-13T01:00:01.000Z"},
		{"deposit_id":"dep_1","deposit_address":"0x93845A25A3a5F9b15c25532797CE56fD4851Fcce","status":"completed",
		 "hold_reason":null,"hold_detail":null,"settlement_id":"stl_1","amount":"2000000",
		 "decimals":6,"recipient":"GBRAD6AQVETCVZDBT3TLU3KXGPQIXOIQX5DLJYKHNZ4ZF6AWYQXEZGQD","delivery_asset":"USDC",
		 "source_chain":"polygon","source_chain_id":80002,"source_tx":"0xdef","destination_tx":"stellarhash",
		 "created_at":"2026-11-13T00:00:00Z","updated_at":"2026-11-13T00:30:00.000Z"}],
		"has_more":true,"next_cursor":"cur_2"}`

	t.Run("first page omits the cursor", func(t *testing.T) {
		t.Parallel()
		svc := newTestCrossMesh(t, serveJSON(t, http.StatusOK, body, func(r *http.Request) {
			assert.Equal(t, "/v1/deposits", r.URL.Path)
			q := r.URL.Query()
			assert.Equal(t, testCrossMeshRecipient, q.Get("recipient"))
			assert.Equal(t, "100", q.Get("limit"))
			assert.False(t, q.Has("starting_after"))
		}))
		got, err := svc.ListDeposits(context.Background(), types.TESTNET, testCrossMeshRecipient, "", 100)
		require.NoError(t, err)
		assert.True(t, got.HasMore)
		require.NotNil(t, got.NextCursor)
		assert.Equal(t, "cur_2", *got.NextCursor)
		require.Len(t, got.Data, 2)

		held := got.Data[0]
		assert.Equal(t, "dep_2", held.DepositID)
		assert.Equal(t, "held", held.Status)
		require.NotNil(t, held.HoldReason)
		assert.Equal(t, "below_minimum", *held.HoldReason)
		require.NotNil(t, held.HoldDetail)
		require.NotNil(t, held.SettlementID)
		assert.Equal(t, "stl_2", *held.SettlementID)
		assert.Equal(t, "500000", held.Amount)
		assert.Equal(t, 6, held.Decimals)
		assert.Equal(t, "polygon", held.SourceChain)
		assert.Equal(t, int64(80002), held.SourceChainID)
		assert.Equal(t, "0xabc", held.SourceTx)
		assert.Nil(t, held.DestinationTx)
		assert.Equal(t, "2026-11-13T01:00:00Z", held.CreatedAt)
		assert.Equal(t, "2026-11-13T01:00:01.000Z", held.UpdatedAt)

		done := got.Data[1]
		assert.Nil(t, done.HoldReason)
		assert.Nil(t, done.HoldDetail)
		require.NotNil(t, done.DestinationTx)
		assert.Equal(t, "stellarhash", *done.DestinationTx)
	})

	t.Run("later page sends the cursor", func(t *testing.T) {
		t.Parallel()
		svc := newTestCrossMesh(t, serveJSON(t, http.StatusOK, `{"data":[],"has_more":false,"next_cursor":null}`, func(r *http.Request) {
			assert.Equal(t, "cur_2", r.URL.Query().Get("starting_after"))
		}))
		got, err := svc.ListDeposits(context.Background(), types.TESTNET, testCrossMeshRecipient, "cur_2", 100)
		require.NoError(t, err)
		assert.False(t, got.HasMore)
		assert.Nil(t, got.NextCursor)
	})
}

func TestCrossMesh_GetSettlement(t *testing.T) {
	t.Parallel()
	body := `{"settlement_id":"stl_2","status":"completed","hold_reason":null,"hold_detail":null,
		"deposit_address":"0x93845A25A3a5F9b15c25532797CE56fD4851Fcce","recipient":"GBRAD6AQVETCVZDBT3TLU3KXGPQIXOIQX5DLJYKHNZ4ZF6AWYQXEZGQD",
		"delivery_asset":"USDC","source_chain":"polygon","source_chain_id":80002,"deposit_count":1,"settled_amount":"2000000",
		"fees":{"setup_fee":"500000","settlement_fee":"100000","cctp_fee":null,"decimals":6},
		"minted_amount":null,"decimals":6,"delivery":{"asset":"USDC","amount":"1400000","decimals":6},
		"executed_speed":"standard","via_sweep":false,"continuation_of":"stl_1","source_tx":"0x1","cctp_mint_tx":"h",
		"destination_tx":"h","created_at":"2026-11-13T00:00:00Z","updated_at":"2026-11-13T00:30:00.000Z"}`
	svc := newTestCrossMesh(t, serveJSON(t, http.StatusOK, body, func(r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/settlements/stl_2", r.URL.Path)
	}))

	got, err := svc.GetSettlement(context.Background(), types.TESTNET, "stl_2")
	require.NoError(t, err)
	assert.Equal(t, "stl_2", got.SettlementID)
	assert.Equal(t, "completed", got.Status)
	assert.Nil(t, got.HoldReason)
	require.NotNil(t, got.Fees)
	assert.Equal(t, "500000", got.Fees.SetupFee)
	assert.Equal(t, "100000", got.Fees.SettlementFee)
	assert.Nil(t, got.Fees.CCTPFee)
	assert.Equal(t, 6, got.Fees.Decimals)
	require.NotNil(t, got.Delivery)
	assert.Equal(t, types.CrossMeshDelivery{Asset: "USDC", Amount: "1400000", Decimals: 6}, *got.Delivery)
	require.NotNil(t, got.ContinuationOf)
	assert.Equal(t, "stl_1", *got.ContinuationOf)
}

func TestCrossMesh_Errors(t *testing.T) {
	t.Parallel()

	t.Run("error envelope decodes with request_id", func(t *testing.T) {
		t.Parallel()
		body := `{"error":{"code":"address_limit_reached","message":"limit","request_id":"req_123"}}`
		svc := newTestCrossMesh(t, serveJSON(t, http.StatusConflict, body, nil))
		_, err := svc.CreateDepositAddress(context.Background(), types.TESTNET, testCrossMeshRecipient)
		var cmErr *CrossMeshError
		require.ErrorAs(t, err, &cmErr)
		assert.Equal(t, CrossMeshError{Status: http.StatusConflict, Code: "address_limit_reached", RequestID: "req_123"}, *cmErr)
		assert.NotContains(t, err.Error(), testCrossMeshKey)
	})

	t.Run("undecodable error body keeps the status", func(t *testing.T) {
		t.Parallel()
		svc := newTestCrossMesh(t, serveJSON(t, http.StatusBadGateway, `<html>bad gateway</html>`, nil))
		_, err := svc.GetChains(context.Background(), types.TESTNET)
		var cmErr *CrossMeshError
		require.ErrorAs(t, err, &cmErr)
		assert.Equal(t, CrossMeshError{Status: http.StatusBadGateway}, *cmErr)
		assert.NotContains(t, err.Error(), testCrossMeshKey)
	})

	t.Run("timeout is distinguishable", func(t *testing.T) {
		t.Parallel()
		release := make(chan struct{})
		svc := newTestCrossMesh(t, func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		t.Cleanup(func() { close(release) })
		svc.timeout = 50 * time.Millisecond

		_, err := svc.GetSettlement(context.Background(), types.TESTNET, "stl_1")
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
		assert.NotContains(t, err.Error(), testCrossMeshKey)
	})

	t.Run("network without an environment", func(t *testing.T) {
		t.Parallel()
		svc := newTestCrossMesh(t, serveJSON(t, http.StatusOK, `{}`, nil))
		_, err := svc.GetChains(context.Background(), types.FUTURENET)
		require.Error(t, err)
	})
}
