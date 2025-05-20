package services

import (
	"context"

	"github.com/stellar/freighter-backend-v2/internal/interfaces"
)

type rpcService struct {
	rpcURL string
}

func NewRPCService(rpcURL string) interfaces.RPCService {
	return &rpcService{rpcURL: rpcURL}
}

func (s *rpcService) GetHealth(ctx context.Context) (string, error) {
	return "OK", nil
}
