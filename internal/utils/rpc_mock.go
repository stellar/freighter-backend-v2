package utils

import (
	"context"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
	"github.com/stellar/stellar-rpc/client"
)

type MockRPCService struct {
	SimulateError    error
	TokenURIOverride string
	GetLedgerEntryOverride []types.LedgerEntryMap
	GetLedgerEntryError error
}

func (m *MockRPCService) ConfigureNetworkClient(network string) *client.Client {
	return nil
}

func (m *MockRPCService) Name() string {
	return "mock-rpc"
}

func (m *MockRPCService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	return types.GetHealthResponse{Status: types.StatusHealthy}, nil
}

func (m *MockRPCService) SimulateTx(ctx context.Context, tx *txnbuild.Transaction, network string) (types.SimulateTransactionResponse, error) {
	return nil, nil
}

func (m *MockRPCService) SimulateInvocation(
	ctx context.Context,
	contractId xdr.ScAddress,
	sourceAccount *txnbuild.SimpleAccount,
	functionName xdr.ScSymbol,
	params []xdr.ScVal,
	timeout txnbuild.TimeBounds,
	network string,
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


func (m *MockRPCService) GetLedgerEntry(ctx context.Context, keys []string, network string) ([]types.LedgerEntryMap, error) {
	if m.GetLedgerEntryOverride != nil {
		return m.GetLedgerEntryOverride, nil
	}

	if (m.GetLedgerEntryError != nil) {
		return nil, m.GetLedgerEntryError
	}
	return nil, nil
}