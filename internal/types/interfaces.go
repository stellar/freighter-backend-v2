package types

import (
	"context"
)

const (
	StatusHealthy   = "healthy"
	StatusUnhealthy = "unhealthy"
	StatusError     = "error"
)

type RPCService interface {
	GetHealth(ctx context.Context) (GetHealthResponse, error)
}

type Service interface {
	Name() string
	GetHealth(ctx context.Context) (GetHealthResponse, error)
}
