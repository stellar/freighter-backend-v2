package utils

import (
	"context"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

type MockRPCService struct {
	SimulateError    error
	TokenURIOverride string
}

func (m *MockRPCService) Name() string {
	return "mock-rpc"
}

func (m *MockRPCService) GetHealth(ctx context.Context) (types.GetHealthResponse, error) {
	return types.GetHealthResponse{Status: types.StatusHealthy}, nil
}

func (m *MockRPCService) SimulateTx(ctx context.Context, tx *txnbuild.Transaction) (types.SimulateTransactionResponse, error) {
	return nil, nil
}

func (m *MockRPCService) SimulateInvocation(
	ctx context.Context,
	contractId xdr.ScAddress,
	sourceAccount *txnbuild.SimpleAccount,
	functionName xdr.ScSymbol,
	params []xdr.ScVal,
	timeout txnbuild.TimeBounds,
) (types.SimulateTransactionResponse, error) {
	if m.SimulateError != nil {
		return nil, m.SimulateError
	}

	fn := string(functionName)

	var result xdr.ScVal
	switch fn {
	case "get_owner_tokens":
		scVec := xdr.ScVec{}
		vecPtr := &scVec
		result = xdr.ScVal{
			Type: xdr.ScValTypeScvVec,
			Vec:  &vecPtr,
		}
	case "owner_of":
		owner := xdr.ScSymbol("GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF")
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &owner}
	case "name":
		name := xdr.ScSymbol("MockNFT")
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &name}
	case "symbol":
		symbol := xdr.ScSymbol("MNFT")
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &symbol}
	case "token_uri":
		uriStr := "https://example.com/token.json"
		if m.TokenURIOverride != "" {
			uriStr = m.TokenURIOverride
		}
		uri := xdr.ScSymbol(uriStr)
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &uri}
	default:
		dummy := xdr.ScSymbol("dummy")
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &dummy}
	}

	return &result, nil
}
