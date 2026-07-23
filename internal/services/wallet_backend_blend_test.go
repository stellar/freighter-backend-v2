// ABOUTME: Tests for the Blend GraphQL client methods against httptest fakes,
// ABOUTME: covering decode (incl. null Floats), auth signing, and error mapping.
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

// graphqlEnvelope wraps data as a GraphQL success response body.
func graphqlEnvelope(t *testing.T, data string) []byte {
	t.Helper()
	return []byte(`{"data":` + data + `}`)
}

func TestGetBlendPositionsDecode(t *testing.T) {
	var gotPath string
	var gotBody wbclient.GraphQLRequest
	svc := newBlendTestService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		// One pool, two reserves. The second reserve is null-heavy: unpriced
		// asset (all USD/APY fields null, priceUsd null) and no registry
		// metadata (tokenName/tokenSymbol/tokenDecimals null) — decode must
		// yield nil pointers, never zeroes.
		_, _ = w.Write(graphqlEnvelope(t, `{
			"accountByAddress": {
				"blendPositions": {
					"pools": [{
						"poolAddress": "CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD",
						"poolName": "Fixed Pool V2",
						"usdValue": 77876.27,
						"suppliedUsd": 674117.02,
						"borrowedUsd": 596240.75,
						"netApy": -0.029,
						"reserves": [
							{
								"assetContractId": "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75",
								"tokenName": "USD Coin",
								"tokenSymbol": "USDC",
								"tokenDecimals": 7,
								"suppliedTokens": "1000000000",
								"collateralTokens": "5563385856000",
								"borrowedTokens": "4953691474632",
								"suppliedUsd": 556438.59,
								"borrowedUsd": 495221.33,
								"supplyApy": 0.0741,
								"borrowApy": 0.1151,
								"emissionsSupplyApr": 0.002,
								"emissionsBorrowApr": 0.001,
								"interestEarned": "6843215",
								"emissionsEarnedBlnd": "12345678",
								"emissionsEarnedUsd": 0.53,
								"priceUsd": 1.0
							},
							{
								"assetContractId": "CBZPEXQLJCUS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI99",
								"tokenName": null,
								"tokenSymbol": null,
								"tokenDecimals": null,
								"suppliedTokens": "68",
								"collateralTokens": "0",
								"borrowedTokens": "0",
								"suppliedUsd": null,
								"borrowedUsd": null,
								"supplyApy": null,
								"borrowApy": null,
								"emissionsSupplyApr": null,
								"emissionsBorrowApr": null,
								"interestEarned": "0",
								"emissionsEarnedBlnd": "0",
								"emissionsEarnedUsd": null,
								"priceUsd": null
							}
						]
					}]
				}
			}
		}`))
	})

	positions, err := svc.GetBlendPositions(context.Background(), blendTestAddress, types.TESTNET)
	require.NoError(t, err)

	assert.Equal(t, wbGraphQLPath, gotPath)
	assert.Contains(t, gotBody.Query, "FreighterBlendPositions")
	assert.Equal(t, blendTestAddress, gotBody.Variables["address"])

	require.Len(t, positions.Pools, 1)
	pool := positions.Pools[0]
	require.NotNil(t, pool.PoolName)
	assert.Equal(t, "Fixed Pool V2", *pool.PoolName)
	require.NotNil(t, pool.NetAPY)
	assert.InDelta(t, -0.029, *pool.NetAPY, 1e-9)

	require.Len(t, pool.Reserves, 2)
	priced, unpriced := pool.Reserves[0], pool.Reserves[1]

	// Token amounts stay full-precision strings.
	assert.Equal(t, "5563385856000", priced.CollateralTokens)
	assert.Equal(t, "6843215", priced.InterestEarned)
	require.NotNil(t, priced.SupplyAPY)
	assert.InDelta(t, 0.0741, *priced.SupplyAPY, 1e-9)
	require.NotNil(t, priced.EmissionsSupplyAPR)
	assert.InDelta(t, 0.002, *priced.EmissionsSupplyAPR, 1e-9)
	require.NotNil(t, priced.EmissionsBorrowAPR)
	assert.InDelta(t, 0.001, *priced.EmissionsBorrowAPR, 1e-9)

	// Null Floats and null registry metadata decode to nil, not zero.
	assert.Nil(t, unpriced.SuppliedUSD)
	assert.Nil(t, unpriced.SupplyAPY)
	assert.Nil(t, unpriced.EmissionsSupplyAPR)
	assert.Nil(t, unpriced.PriceUSD)
	assert.Nil(t, unpriced.TokenSymbol)
	assert.Nil(t, unpriced.TokenDecimals)
	assert.Equal(t, "68", unpriced.SuppliedTokens)
}

func TestGetBlendPositionsUnknownAccountIsEmpty(t *testing.T) {
	svc := newBlendTestService(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(graphqlEnvelope(t, `{"accountByAddress": null}`))
	})

	positions, err := svc.GetBlendPositions(context.Background(), blendTestAddress, types.TESTNET)
	require.NoError(t, err)
	require.NotNil(t, positions)
	assert.NotNil(t, positions.Pools)
	assert.Empty(t, positions.Pools)
}

func TestGetBlendPoolsDecode(t *testing.T) {
	var gotBody wbclient.GraphQLRequest
	svc := newBlendTestService(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_, _ = w.Write(graphqlEnvelope(t, `{
			"blendPools": [
				{
					"address": "CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD",
					"name": null,
					"status": "ACTIVE",
					"suppliedUsd": 2100000.5,
					"borrowedUsd": 900000.25,
					"interestApy": 0.043,
					"netApy": 0.047,
					"reserves": [{
						"assetContractId": "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75",
						"tokenName": "USD Coin",
						"tokenSymbol": "USDC",
						"tokenDecimals": 7,
						"enabled": true,
						"utilization": 0.62,
						"supplyApy": 0.043,
						"borrowApy": 0.061,
						"emissionsSupplyApr": 0.008,
						"suppliedUsd": 1500000.0,
						"borrowedUsd": 930000.0,
						"priceUsd": 1.0
					}]
				},
				{
					"address": "CCCCIQSDILITHMM7PBSLVDT5MISSY7R26MNZXCX4H7J5JQ5FPIYOGYFS",
					"name": "Second Pool",
					"status": null,
					"suppliedUsd": null,
					"borrowedUsd": null,
					"interestApy": null,
					"netApy": null,
					"reserves": []
				}
			]
		}`))
	})

	pools, err := svc.GetBlendPools(context.Background(), types.TESTNET)
	require.NoError(t, err)
	assert.Contains(t, gotBody.Query, "FreighterBlendPools")

	require.Len(t, pools, 2)
	require.NotNil(t, pools[0].Status)
	assert.Equal(t, types.BlendPoolStatusActive, *pools[0].Status)
	assert.Nil(t, pools[0].Name)
	require.Len(t, pools[0].Reserves, 1)
	assert.True(t, pools[0].Reserves[0].Enabled)

	// Not-yet-ingested pool: status and all totals null.
	assert.Nil(t, pools[1].Status)
	assert.Nil(t, pools[1].SuppliedUSD)
	assert.Empty(t, pools[1].Reserves)
}

func TestBlendGraphQLErrorClassification(t *testing.T) {
	t.Run("GraphQL errors array becomes graphql_error", func(t *testing.T) {
		svc := newBlendTestService(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"blendPools\" on type \"Query\"."}]}`))
		})

		_, err := svc.GetBlendPools(context.Background(), types.TESTNET)
		require.Error(t, err)
		var upErr *metrics.UpstreamError
		require.ErrorAs(t, err, &upErr)
		assert.Equal(t, "graphql_error", upErr.Kind)
		assert.Contains(t, err.Error(), "blendPools")
	})

	t.Run("non-200 becomes http_error with code", func(t *testing.T) {
		svc := newBlendTestService(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream sad"))
		})

		_, err := svc.GetBlendPositions(context.Background(), blendTestAddress, types.TESTNET)
		require.Error(t, err)
		var upErr *metrics.UpstreamError
		require.ErrorAs(t, err, &upErr)
		assert.Equal(t, "http_error", upErr.Kind)
		assert.Equal(t, http.StatusBadGateway, upErr.Code)
	})

	t.Run("malformed data payload is a decode error", func(t *testing.T) {
		svc := newBlendTestService(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data": {"blendPools": "not-a-list"}}`))
		})

		_, err := svc.GetBlendPools(context.Background(), types.TESTNET)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling GetBlendPools data")
	})
}

// headerSigner is a fake auth.HTTPRequestSigner that stamps a header so the
// test can assert the signing hook runs for raw GraphQL documents.
type headerSigner struct{}

func (headerSigner) SignHTTPRequest(req *http.Request, _ time.Duration) error {
	req.Header.Set("Authorization", "Bearer test-jwt")
	return nil
}

func TestBlendGraphQLRequestIsSigned(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":{"blendPools":[]}}`))
	}))
	t.Cleanup(server.Close)

	client := wbclient.NewClient(server.URL, headerSigner{})
	svc := &walletBackendService{testnetClient: client, maxBalanceConcurrency: 1}

	pools, err := svc.GetBlendPools(context.Background(), types.TESTNET)
	require.NoError(t, err)
	assert.Empty(t, pools)
	assert.NotNil(t, pools)
	assert.Equal(t, "Bearer test-jwt", gotAuth)
}
