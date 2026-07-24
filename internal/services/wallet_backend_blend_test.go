// ABOUTME: Tests for the Blend wallet-backend service methods, exercising the
// ABOUTME: wrapper policies (normalization, error classification) through fake servers.
package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/wallet-backend/pkg/wbclient"
	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const blendTestAddress = "GDW6QB3BFPQ3I4LH752JD2HYADFM2T4RVRCEUNCCH7MICWZR67NL5552"

// newBlendTestService wires a walletBackendService whose testnet client
// points at the given httptest server, with no request signer.
func newBlendTestService(t *testing.T, handler http.HandlerFunc) *walletBackendService {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &walletBackendService{
		testnetClient:         wbclient.NewClient(server.URL, nil),
		maxBalanceConcurrency: 1,
	}
}

func TestGetBlendPositions(t *testing.T) {
	ctx := context.Background()

	t.Run("decodes positions through the SDK", func(t *testing.T) {
		svc := newBlendTestService(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data": {
				"accountByAddress": {
					"blendPositions": {
						"pools": [{
							"poolAddress": "CCEBVDYM32YNYCVNRXQKDFFPISJJCV557CDZEIRBEE4NCV4KHPQ44HGF",
							"poolName": "TestnetV2",
							"usdValue": 3619.27,
							"suppliedUsd": 3619.29,
							"borrowedUsd": 0.02,
							"netApy": 0.025,
							"claimedBlnd": "0",
							"reserves": [{
								"assetContractId": "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
								"tokenName": null,
								"tokenSymbol": null,
								"tokenDecimals": 7,
								"suppliedTokens": "0",
								"collateralTokens": "67125489343",
								"borrowedTokens": "0",
								"suppliedUsd": 2819.27,
								"borrowedUsd": null,
								"supplyApy": 3.24,
								"borrowApy": null,
								"emissionsSupplyApr": 0,
								"emissionsBorrowApr": null,
								"interestEarned": "2125489343",
								"interestPaid": "0",
								"emissionsEarnedBlnd": "0",
								"emissionsEarnedUsd": null,
								"priceUsd": 0.42
							}]
						}],
						"backstop": [],
						"backstopClaimedLp": "0"
					}
				}
			}}`))
		})

		positions, err := svc.GetBlendPositions(ctx, blendTestAddress, types.TESTNET)
		require.NoError(t, err)

		require.Len(t, positions.Pools, 1)
		pool := positions.Pools[0]
		require.NotNil(t, pool.NetApy)
		assert.InDelta(t, 0.025, *pool.NetApy, 1e-9)
		require.Len(t, pool.Reserves, 1)
		reserve := pool.Reserves[0]
		// Registry gaps and unpriced sides decode to nil, never zero.
		assert.Nil(t, reserve.TokenSymbol)
		assert.Nil(t, reserve.EmissionsBorrowApr)
		assert.Equal(t, "67125489343", reserve.CollateralTokens)
		assert.Equal(t, "2125489343", reserve.InterestEarned)
	})

	t.Run("unknown account normalizes to empty positions", func(t *testing.T) {
		svc := newBlendTestService(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data": {"accountByAddress": null}}`))
		})

		positions, err := svc.GetBlendPositions(ctx, blendTestAddress, types.TESTNET)
		require.NoError(t, err)
		require.NotNil(t, positions)
		assert.NotNil(t, positions.Pools)
		assert.Empty(t, positions.Pools)
	})
}

func TestGetBlendPools(t *testing.T) {
	ctx := context.Background()

	svc := newBlendTestService(t, func(w http.ResponseWriter, r *http.Request) {
		var req wbclient.GraphQLRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Contains(t, req.Query, "blendPools")
		_, _ = w.Write([]byte(`{"data": {"blendPools": [{
			"address": "CCEBVDYM32YNYCVNRXQKDFFPISJJCV557CDZEIRBEE4NCV4KHPQ44HGF",
			"name": "TestnetV2",
			"status": "ACTIVE",
			"suppliedUsd": 2100000.5,
			"reserves": []
		}]}}`))
	})

	pools, err := svc.GetBlendPools(ctx, types.TESTNET)
	require.NoError(t, err)
	require.Len(t, pools, 1)
	require.NotNil(t, pools[0].Status)
	assert.Equal(t, wbtypes.BlendPoolStatusActive, *pools[0].Status)
	assert.True(t, pools[0].Status.AcceptsSupply())
}

func TestBlendErrorClassification(t *testing.T) {
	ctx := context.Background()

	t.Run("GraphQL errors classify as graphql_error", func(t *testing.T) {
		svc := newBlendTestService(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"blendPools\" on type \"Query\"."}]}`))
		})

		_, err := svc.GetBlendPools(ctx, types.TESTNET)
		require.Error(t, err)
		var upErr *metrics.UpstreamError
		require.ErrorAs(t, err, &upErr)
		assert.Equal(t, "graphql_error", upErr.Kind)
	})

	t.Run("non-200 classifies as http_error with code", func(t *testing.T) {
		svc := newBlendTestService(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream sad"))
		})

		_, err := svc.GetBlendPositions(ctx, blendTestAddress, types.TESTNET)
		require.Error(t, err)
		var upErr *metrics.UpstreamError
		require.ErrorAs(t, err, &upErr)
		assert.Equal(t, "http_error", upErr.Kind)
		assert.Equal(t, http.StatusBadGateway, upErr.Code)
	})

	t.Run("unconfigured network errors without a request", func(t *testing.T) {
		svc := &walletBackendService{maxBalanceConcurrency: 1}
		_, err := svc.GetBlendPools(ctx, types.PUBLIC)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not configured")
	})
}

// headerSigner is a fake auth.HTTPRequestSigner that stamps a header so the
// test can assert the SDK's signing hook runs for Blend calls.
type headerSigner struct{}

func (headerSigner) SignHTTPRequest(req *http.Request, _ time.Duration) error {
	req.Header.Set("Authorization", "Bearer test-jwt")
	return nil
}

func TestBlendRequestIsSigned(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":{"blendPools":[]}}`))
	}))
	t.Cleanup(server.Close)

	svc := &walletBackendService{
		testnetClient:         wbclient.NewClient(server.URL, headerSigner{}),
		maxBalanceConcurrency: 1,
	}

	pools, err := svc.GetBlendPools(context.Background(), types.TESTNET)
	require.NoError(t, err)
	assert.NotNil(t, pools)
	assert.Empty(t, pools)
	assert.Equal(t, "Bearer test-jwt", gotAuth)
}
