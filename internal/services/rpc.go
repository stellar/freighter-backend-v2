package services

import (
	"context"
	"fmt"
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
	"github.com/stellar/stellar-rpc/client"
	"github.com/stellar/stellar-rpc/protocol"
)

const (
	serviceName = "rpc"
)

type rpcService struct {
	client *client.Client
}

func NewRPCService(rpcURL string) types.Service {
	return &rpcService{
		client: client.NewClient(rpcURL, &http.Client{}),
	}
}

func (r *rpcService) Name() string {
	return serviceName
}

func (r *rpcService) GetHealth(ctx context.Context) (types.GetHealthResponse, error) {
	response, err := r.client.GetHealth(ctx)
	if err != nil {
		return types.GetHealthResponse{Status: types.StatusError}, err
	}

	return types.GetHealthResponse{
		Status: response.Status,
	}, nil
}

func (r *rpcService) SimulateTx(
	ctx context.Context,
	tx *txnbuild.Transaction,
) (types.SimulateTransactionResponse, error) {
	txeB64, err := tx.Base64()
	if err != nil {
		return nil, fmt.Errorf("could not encode transaction: %w", err)
	}

	resp, err := r.client.SimulateTransaction(ctx, protocol.SimulateTransactionRequest{
		Transaction: txeB64,
	})
	if err != nil {
		return nil, fmt.Errorf("simulateTransaction RPC failed: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("simulateTransaction returned error: %s", resp.Error)
	}
	if len(resp.Results) == 0 {
		return nil, fmt.Errorf("no results in simulation")
	}

	if resp.Results[0].ReturnValueXDR == nil {
		return nil, fmt.Errorf("simulateTransaction result has nil ReturnValueXDR")
	}

	var retval xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(*resp.Results[0].ReturnValueXDR, &retval); err != nil {
		return nil, fmt.Errorf("failed to decode result XDR: %w", err)
	}

	return &retval, nil
}
