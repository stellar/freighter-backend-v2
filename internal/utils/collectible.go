package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

type Collection struct {
	Name   string `json:"name"`
	Symbol string `json:"symbol"`
}

type Collectible struct {
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	ImageURL    string `json:"image"`
	Description string `json:"description"`
	TokenUri    string `json:"token_uri"`
	TokenId     string `json:"token_id"`
}

type TokenMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Issuer      string `json:"issuer"`
}

func FetchCollection(
	rpc types.RPCService,
	ctx context.Context,
	accountId *txnbuild.SimpleAccount,
	contractID string,
) (*Collection, error) {
	id, err := ScAddressFromString(contractID)
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

	return &Collection{
		Name:   nameRes.value,
		Symbol: symbolRes.value,
	}, nil
}

func FetchCollectible(
	rpc types.RPCService,
	ctx context.Context,
	accountId *txnbuild.SimpleAccount,
	contractID string,
	tokenId string,
	client *http.Client,
) (*Collectible, error) {

	id, err := ScAddressFromString(contractID)
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

	resp, err := client.Get(tokenURIRes.val.String())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch token metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token metadata request returned status %d", resp.StatusCode)
	}

	var meta TokenMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("failed to decode token metadata: %w", err)
	}

	return &Collectible{
		Owner:       ownerRes.val.String(),
		Name:        meta.Name,
		TokenUri:    tokenURIRes.val.String(),
		ImageURL:    meta.URL,
		Description: meta.Description,
		TokenId:     tokenId,
	}, nil
}
