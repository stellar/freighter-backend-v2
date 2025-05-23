// ABOUTME: Contains shared interfaces used throughout the application.
// ABOUTME: Defines interfaces to prevent import cycles between packages.

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
