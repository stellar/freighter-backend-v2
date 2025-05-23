package services

import (
	"context"
	"net/http"

	"github.com/stellar/stellar-rpc/client"

	"github.com/stellar/freighter-backend-v2/internal/types"
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
