package utils

import (
	"context"
	"strconv"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

type Collectible struct {
	Owner    string `json:"owner"`
	TokenUri string `json:"token_uri"`
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
}

func FetchCollectible(
	rpc types.RPCService, ctx context.Context, accountId *txnbuild.SimpleAccount, contractID string, tokenId string,
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

	owner, err := rpc.InvokeContract(ctx, *id, accountId, "owner_of", []xdr.ScVal{scToken}, txnbuild.NewTimeout(300))
	if err != nil {
		return nil, err
	}
	name, err := rpc.InvokeContract(ctx, *id, accountId, "name", []xdr.ScVal{}, txnbuild.NewTimeout(300))
	if err != nil {
		return nil, err
	}
	symbol, err := rpc.InvokeContract(ctx, *id, accountId, "symbol", []xdr.ScVal{}, txnbuild.NewTimeout(300))
	if err != nil {
		return nil, err
	}
	tokenURI, err := rpc.InvokeContract(ctx, *id, accountId, "token_uri", []xdr.ScVal{scToken}, txnbuild.NewTimeout(300))
	if err != nil {
		return nil, err
	}

	return &Collectible{
		Owner:    owner.String(),
		Name:     name.String(),
		Symbol:   symbol.String(),
		TokenUri: tokenURI.String(),
	}, nil
}
