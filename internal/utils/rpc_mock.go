package utils

import (
	"context"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

type MockRPCService struct {
	SimulateError error
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

// Correct signature to implement types.RPCService
func (m *MockRPCService) InvokeContract(
	ctx context.Context,
	contractId xdr.ScAddress,
	sourceAccount *txnbuild.SimpleAccount,
	functionName xdr.ScSymbol, // <- matches interface
	params []xdr.ScVal,
	timeout txnbuild.TimeBounds,
) (types.SimulateTransactionResponse, error) { // <- matches interface
	if m.SimulateError != nil {
		return nil, m.SimulateError
	}

	fn := string(functionName) // convert ScSymbol to string for convenience

	var result xdr.ScVal
	switch fn {
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
		uri := xdr.ScSymbol("https://example.com/token.json")
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &uri}
	default:
		dummy := xdr.ScSymbol("dummy")
		result = xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &dummy}
	}

	return &result, nil // return xdr.ScVal wrapped in types.SimulateTransactionResponse
}
