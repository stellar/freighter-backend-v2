// ABOUTME: Contains shared interfaces used throughout the application.
// ABOUTME: Defines interfaces to prevent import cycles between packages.

package interfaces

import (
	"context"
)

// RPCService defines the interface for RPC-related operations
type RPCService interface {
	GetHealth(ctx context.Context) (string, error)
}
