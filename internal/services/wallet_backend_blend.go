// ABOUTME: Blend methods on the wallet-backend service: account positions and
// ABOUTME: the pool catalog, via the wbclient SDK's typed Blend API.
package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/stellar/wallet-backend/pkg/wbclient"
	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"
)

// GetBlendPositions returns the account's Blend positions. The SDK reports an
// account unknown to the indexer as wbclient.ErrAccountNotFound; for this
// read, "unknown account" and "no positions" are the same client-facing fact,
// so both normalize to empty positions.
func (w *walletBackendService) GetBlendPositions(ctx context.Context, address, network string) (_ *wbtypes.BlendAccountPositions, err error) {
	start := time.Now()
	defer func() { w.recordWBCall("GetBlendPositions", network, start, err) }()

	client := w.configureNetworkClient(network)
	if client == nil {
		return nil, fmt.Errorf("wallet backend client not configured for network: %s", network)
	}

	positions, err := client.GetAccountBlendPositions(ctx, address)
	if err != nil {
		if errors.Is(err, wbclient.ErrAccountNotFound) {
			return &wbtypes.BlendAccountPositions{Pools: []wbtypes.BlendPoolPosition{}}, nil
		}
		return nil, classifyWBError(err)
	}
	if positions.Pools == nil {
		positions.Pools = []wbtypes.BlendPoolPosition{}
	}
	return positions, nil
}

// GetBlendPools returns the pool-wide Blend catalog. Always a non-nil slice.
func (w *walletBackendService) GetBlendPools(ctx context.Context, network string) (_ []wbtypes.BlendPool, err error) {
	start := time.Now()
	defer func() { w.recordWBCall("GetBlendPools", network, start, err) }()

	client := w.configureNetworkClient(network)
	if client == nil {
		return nil, fmt.Errorf("wallet backend client not configured for network: %s", network)
	}

	pools, err := client.GetBlendPools(ctx)
	if err != nil {
		return nil, classifyWBError(err)
	}
	if pools == nil {
		return []wbtypes.BlendPool{}, nil
	}
	return pools, nil
}
