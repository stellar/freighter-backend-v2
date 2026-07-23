// ABOUTME: Blend GraphQL queries against wallet-backend: account positions and
// ABOUTME: the pool catalog, via a hand-rolled signed GraphQL POST.
package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/stellar/wallet-backend/pkg/wbclient"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// The Blend queries are issued as raw GraphQL documents over wbclient's
// transport pieces (BaseURL, JWT RequestSigner, shared HTTPClient) because
// the SDK's own executeGraphQL is unexported and has no Blend methods yet.
// Field lists mirror internal/serve/graphql/schema/blend.graphqls on the
// wallet-backend Blend branch; re-verify them against the merged schema
// when that stack lands.
const (
	wbGraphQLPath = "/graphql/query"

	// wbSignTimeout matches the JWT expiry wbclient.Client.request uses.
	wbSignTimeout = 5 * time.Second

	blendPositionsQuery = `query FreighterBlendPositions($address: String!) {
  accountByAddress(address: $address) {
    blendPositions {
      pools {
        poolAddress
        poolName
        usdValue
        suppliedUsd
        borrowedUsd
        netApy
        reserves {
          assetContractId
          tokenName
          tokenSymbol
          tokenDecimals
          suppliedTokens
          collateralTokens
          borrowedTokens
          suppliedUsd
          borrowedUsd
          supplyApy
          borrowApy
          emissionsSupplyApr
          emissionsBorrowApr
          interestEarned
          emissionsEarnedBlnd
          emissionsEarnedUsd
          priceUsd
        }
      }
    }
  }
}`

	blendPoolsQuery = `query FreighterBlendPools {
  blendPools {
    address
    name
    status
    suppliedUsd
    borrowedUsd
    interestApy
    netApy
    reserves {
      assetContractId
      tokenName
      tokenSymbol
      tokenDecimals
      enabled
      utilization
      supplyApy
      borrowApy
      emissionsSupplyApr
      suppliedUsd
      borrowedUsd
      priceUsd
    }
  }
}`
)

// Root-field wrappers for each query document.
type blendPositionsData struct {
	AccountByAddress *struct {
		BlendPositions types.BlendAccountPositions `json:"blendPositions"`
	} `json:"accountByAddress"`
}

type blendPoolsData struct {
	BlendPools []types.BlendPool `json:"blendPools"`
}

// GetBlendPositions returns the account's Blend positions. accountByAddress
// resolving to null (an account wallet-backend has never indexed) returns
// empty positions rather than an error: for this read, "unknown account" and
// "no positions" are the same client-facing fact.
func (w *walletBackendService) GetBlendPositions(ctx context.Context, address, network string) (_ *types.BlendAccountPositions, err error) {
	start := time.Now()
	defer func() { w.recordWBCall("GetBlendPositions", network, start, err) }()

	data, err := wbGraphQL[blendPositionsData](ctx, w, network, "GetBlendPositions", blendPositionsQuery, map[string]interface{}{"address": address})
	if err != nil {
		return nil, err
	}
	if data.AccountByAddress == nil {
		return &types.BlendAccountPositions{Pools: []types.BlendPoolPosition{}}, nil
	}
	positions := data.AccountByAddress.BlendPositions
	if positions.Pools == nil {
		positions.Pools = []types.BlendPoolPosition{}
	}
	return &positions, nil
}

// GetBlendPools returns the pool-wide catalog. Always a non-nil slice.
func (w *walletBackendService) GetBlendPools(ctx context.Context, network string) (_ []types.BlendPool, err error) {
	start := time.Now()
	defer func() { w.recordWBCall("GetBlendPools", network, start, err) }()

	data, err := wbGraphQL[blendPoolsData](ctx, w, network, "GetBlendPools", blendPoolsQuery, nil)
	if err != nil {
		return nil, err
	}
	if data.BlendPools == nil {
		return []types.BlendPool{}, nil
	}
	return data.BlendPools, nil
}

// wbGraphQL executes one GraphQL document against the network's
// wallet-backend and unmarshals the response's data into T. It mirrors
// wbclient.Client.request (same path, JWT signing, and error vocabulary:
// "unexpected statusCode=" / "GraphQL error:" so classifyWBError and the
// handlers' translateServiceError treat raw-document calls exactly like SDK
// calls). A free function because Go methods cannot be generic.
func wbGraphQL[T any](ctx context.Context, w *walletBackendService, network, method, query string, variables map[string]interface{}) (*T, error) {
	client := w.configureNetworkClient(network)
	if client == nil {
		return nil, fmt.Errorf("wallet backend client not configured for network: %s", network)
	}

	body, err := json.Marshal(wbclient.GraphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, fmt.Errorf("marshalling %s request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.BaseURL+wbGraphQLPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating %s request: %w", method, err)
	}
	if client.RequestSigner != nil {
		if signErr := client.RequestSigner.SignHTTPRequest(req, wbSignTimeout); signErr != nil {
			return nil, fmt.Errorf("signing %s request: %w", method, signErr)
		}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.HTTPClient.Do(req)
	if err != nil {
		return nil, classifyWBError(fmt.Errorf("sending %s request: %w", method, err))
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, classifyWBError(fmt.Errorf("%s: unexpected statusCode=%d, body=%s", method, resp.StatusCode, snippet))
	}

	var envelope wbclient.GraphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("parsing %s response body: %w", method, err)
	}
	if len(envelope.Errors) > 0 {
		return nil, classifyWBError(fmt.Errorf("%s: GraphQL error: %s", method, envelope.Errors[0].Message))
	}

	var data T
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, fmt.Errorf("unmarshaling %s data: %w", method, err)
	}
	return &data, nil
}
