package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/alitto/pond/v2"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stellar/wallet-backend/pkg/wbclient"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

// isValidWalletBackendNetwork reports whether network is one of the values
// freighter-backend has a wallet-backend client configured for. WalletBackendConfig
// only carries pubnet and testnet URL/signing-key fields, so FUTURENET (which is
// otherwise a valid Stellar network in this codebase) is intentionally rejected
// to prevent silent fall-through to the pubnet client.
func isValidWalletBackendNetwork(network string) bool {
	return network == types.PUBLIC || network == types.TESTNET
}

// translateServiceError maps a service-layer error to a typed HttpError per
// the spec's REST-honest mapping. Logs all non-404 errors with context;
// account-not-found is a normal client outcome and is not logged.
//
// Status mapping:
//   - wbclient.ErrAccountNotFound       -> 404
//   - context.DeadlineExceeded          -> 504 (server-side timeout)
//   - context.Canceled                  -> 503 (client disconnect / parent abort)
//   - *metrics.UpstreamError (any Kind) -> 502 (graphql_error, http_error)
//   - *url.Error / *net.OpError         -> 502 (transport / DNS / dial)
//   - anything else                     -> 500
func translateServiceError(ctx context.Context, err error, resource, address, network string) *httperror.HttpError {
	switch {
	case errors.Is(err, wbclient.ErrAccountNotFound):
		return httperror.NotFound(fmt.Sprintf("%s not found", resource), err)
	case errors.Is(err, context.DeadlineExceeded):
		logger.ErrorWithContext(ctx, "wallet-backend call timed out", "resource", resource, "address", address, "network", network, "error", err)
		return httperror.GatewayTimeout(fmt.Sprintf("Failed to get %s", resource), err)
	case errors.Is(err, context.Canceled):
		logger.ErrorWithContext(ctx, "wallet-backend call canceled by client", "resource", resource, "address", address, "network", network, "error", err)
		return httperror.ServiceUnavailable(fmt.Sprintf("Failed to get %s", resource), err)
	}
	var upErr *metrics.UpstreamError
	if errors.As(err, &upErr) {
		logger.ErrorWithContext(ctx, "wallet-backend upstream error", "resource", resource, "address", address, "network", network, "kind", upErr.Kind, "code", upErr.Code, "error", err)
		return httperror.BadGateway(fmt.Sprintf("Failed to get %s", resource), err)
	}
	var urlErr *url.Error
	var netErr *net.OpError
	if errors.As(err, &urlErr) || errors.As(err, &netErr) {
		logger.ErrorWithContext(ctx, "wallet-backend transport error", "resource", resource, "address", address, "network", network, "error", err)
		return httperror.BadGateway(fmt.Sprintf("Failed to get %s", resource), err)
	}
	logger.ErrorWithContext(ctx, "wallet-backend call failed", "resource", resource, "address", address, "network", network, "error", err)
	return httperror.InternalServerError(fmt.Sprintf("Failed to get %s", resource), err)
}

type collection struct {
	Name   string `json:"name"`
	Symbol string `json:"symbol"`
}

type Collectible struct {
	Owner    string `json:"owner"`
	TokenUri string `json:"token_uri"`
	TokenId  string `json:"token_id"`
}

func FetchCollection(
	rpc types.RPCService,
	ctx context.Context,
	accountId *txnbuild.SimpleAccount,
	contractID string,
	network string,
	rpcPool pond.Pool,
) (*collection, error) {
	id, err := utils.ScAddressFromString(contractID)
	if err != nil {
		return nil, err
	}

	type result struct {
		value string
		err   error
	}

	nameCh := make(chan result, 1)
	symbolCh := make(chan result, 1)

	// Use rpcPool to bound concurrent RPC calls
	group := rpcPool.NewGroupContext(ctx)

	group.Submit(func() {
		res, err := rpc.SimulateInvocation(ctx, *id, accountId, "name", []xdr.ScVal{}, txnbuild.NewTimeout(300), network)
		if err != nil {
			nameCh <- result{"", err}
			return
		}
		nameCh <- result{res.String(), nil}
	})

	group.Submit(func() {
		res, err := rpc.SimulateInvocation(ctx, *id, accountId, "symbol", []xdr.ScVal{}, txnbuild.NewTimeout(300), network)
		if err != nil {
			symbolCh <- result{"", err}
			return
		}
		symbolCh <- result{res.String(), nil}
	})

	if err := group.Wait(); err != nil {
		return nil, err
	}

	// Use context-aware channel reads to prevent blocking forever
	var nameRes, symbolRes result

	select {
	case nameRes = <-nameCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case symbolRes = <-symbolCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if nameRes.err != nil {
		return nil, nameRes.err
	}
	if symbolRes.err != nil {
		return nil, symbolRes.err
	}

	return &collection{
		Name:   nameRes.value,
		Symbol: symbolRes.value,
	}, nil
}

func fetchCollectible(
	rpc types.RPCService,
	ctx context.Context,
	accountId *txnbuild.SimpleAccount,
	contractID string,
	tokenId string,
	network string,
	rpcPool pond.Pool,
) (*Collectible, error) {
	id, err := utils.ScAddressFromString(contractID)
	if err != nil {
		return nil, err
	}

	tokenUint, err := strconv.ParseUint(tokenId, 10, 32)
	if err != nil {
		return nil, err
	}
	tokenVal := xdr.Uint32(tokenUint)
	scToken := xdr.ScVal{
		Type: xdr.ScValTypeScvU32,
		U32:  &tokenVal,
	}

	// Use rpcPool to bound concurrent RPC calls
	type result struct {
		val xdr.ScVal
		err error
	}
	ownerCh := make(chan result, 1)
	tokenURICh := make(chan result, 1)

	group := rpcPool.NewGroupContext(ctx)

	group.Submit(func() {
		res, err := rpc.SimulateInvocation(ctx, *id, accountId, "owner_of", []xdr.ScVal{scToken}, txnbuild.NewTimeout(300), network)
		if err != nil {
			ownerCh <- result{xdr.ScVal{}, err}
			return
		}
		ownerCh <- result{*res, nil}
	})

	group.Submit(func() {
		res, err := rpc.SimulateInvocation(ctx, *id, accountId, "token_uri", []xdr.ScVal{scToken}, txnbuild.NewTimeout(300), network)
		if err != nil {
			tokenURICh <- result{xdr.ScVal{}, err}
			return
		}
		tokenURICh <- result{*res, nil}
	})

	if err := group.Wait(); err != nil {
		return nil, err
	}

	// Use context-aware channel reads to prevent blocking forever
	var ownerRes, tokenURIRes result

	select {
	case ownerRes = <-ownerCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case tokenURIRes = <-tokenURICh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if ownerRes.err != nil {
		return nil, ownerRes.err
	}
	if tokenURIRes.err != nil {
		return nil, tokenURIRes.err
	}

	return &Collectible{
		Owner:    ownerRes.val.String(),
		TokenUri: tokenURIRes.val.String(),
		TokenId:  tokenId,
	}, nil
}

/*
NOTE: This function is not part of SEP-50 and should only be used with Meridian Pay NFTs or contracts that extend the standard.
*/
func fetchOwnerTokens(
	rpc types.RPCService,
	ctx context.Context,
	accountId *txnbuild.SimpleAccount,
	contractID string,
	owner string,
	network string,
) ([]string, error) {
	id, err := utils.ScAddressFromString(contractID)
	if err != nil {
		return nil, err
	}

	ownerAddress, err := utils.ScAddressFromString(owner)
	if err != nil {
		return nil, err
	}

	ownerVal := xdr.ScVal{
		Type:    xdr.ScValTypeScvAddress,
		Address: ownerAddress,
	}

	// Make direct RPC call (already running within a pool task from caller)
	res, err := rpc.SimulateInvocation(ctx, *id, accountId, "get_owner_tokens", []xdr.ScVal{ownerVal}, txnbuild.NewTimeout(300), network)
	if err != nil {
		return nil, err
	}

	vec, ok := res.GetVec()
	if !ok {
		return nil, fmt.Errorf("expected SCV_VEC result, got %v", res.Type)
	}

	tokenIDs, err := utils.ScVecToStrings(vec)
	if err != nil {
		return nil, err
	}

	return tokenIDs, nil
}
