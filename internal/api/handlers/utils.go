package handlers

import (
	"context"
	"strconv"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

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

	go func() {
		res, err := rpc.SimulateInvocation(ctx, *id, accountId, "name", []xdr.ScVal{}, txnbuild.NewTimeout(300))
		if err != nil {
			nameCh <- result{"", err}
			return
		}
		nameCh <- result{res.String(), nil}
	}()

	go func() {
		res, err := rpc.SimulateInvocation(ctx, *id, accountId, "symbol", []xdr.ScVal{}, txnbuild.NewTimeout(300))
		if err != nil {
			symbolCh <- result{"", err}
			return
		}
		symbolCh <- result{res.String(), nil}
	}()

	nameRes := <-nameCh
	symbolRes := <-symbolCh

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

	type result struct {
		val xdr.ScVal
		err error
	}
	ownerCh := make(chan result, 1)
	tokenURICh := make(chan result, 1)

	go func() {
		res, err := rpc.SimulateInvocation(ctx, *id, accountId, "owner_of", []xdr.ScVal{scToken}, txnbuild.NewTimeout(300))
		if err != nil {
			ownerCh <- result{xdr.ScVal{}, err}
			return
		}
		ownerCh <- result{*res, nil}
	}()

	go func() {
		res, err := rpc.SimulateInvocation(ctx, *id, accountId, "token_uri", []xdr.ScVal{scToken}, txnbuild.NewTimeout(300))
		if err != nil {
			tokenURICh <- result{xdr.ScVal{}, err}
			return
		}
		tokenURICh <- result{*res, nil}
	}()

	ownerRes := <-ownerCh
	tokenURIRes := <-tokenURICh

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
) ([]string, error) {

	id, err := utils.ScAddressFromString(contractID)
	if err != nil {
		return nil, err
	}

	ownerAddress, err := utils.ScAddressFromString(owner)
	if err != nil {
		return nil, err
	}

	type result struct {
		val xdr.ScVal
		err error
	}
	ownerTokensCh := make(chan result, 1)
	ownerVal := xdr.ScVal{
		Type:    xdr.ScValTypeScvAddress,
		Address: ownerAddress,
	}

	go func() {
		res, err := rpc.SimulateInvocation(ctx, *id, accountId, "get_owner_tokens", []xdr.ScVal{ownerVal}, txnbuild.NewTimeout(300))
		if err != nil {
			ownerTokensCh <- result{xdr.ScVal{}, err}
			return
		}
		ownerTokensCh <- result{*res, nil}
	}()

	ownerTokensRes := <-ownerTokensCh

	if ownerTokensRes.err != nil {
		return nil, ownerTokensRes.err
	}

	tokenIDs, err := utils.ScVecToStrings(*ownerTokensRes.val.Vec)
	if err != nil {
		return nil, err
	}

	return tokenIDs, nil
}


func FetchHomeDomains(
	rpc types.RPCService,
	ctx context.Context,
	ledgerKeyAccountKeys []string,
	network string,
) ([]types.LedgerEntryMap, error) {
	data, e := rpc.GetLedgerEntry(ctx, ledgerKeyAccountKeys, network)
	if e != nil {
		return nil, e
	}
	return data, e
}
