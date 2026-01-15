package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	StatusHealthy = "healthy"
	healthTimeout = 5 * time.Second
)

// HealthResponse contains the health status of the service.
type HealthResponse struct {
	Status     string                       `json:"status"`
	RPCHealth  types.GetHealthResponse `json:"rpc_health,omitempty"`
}

type HealthHandler struct {
	rpcService types.RPCService
}

func NewHealthHandler(rpcService types.RPCService) *HealthHandler {
	return &HealthHandler{
		rpcService: rpcService,
	}
}

// CheckHealth handles health check requests.
func (h *HealthHandler) CheckHealth(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	// Get network from query parameter, default to PUBLIC
	network := r.URL.Query().Get("network")
	if network == "" {
		network = types.PUBLIC
	}

	// Validate network
	if !isValidNetwork(network) {
		return httperror.BadRequest("invalid network parameter", nil)
	}

	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()

	// Check RPC health for the requested network
	rpcHealth := h.checkRPCHealth(ctx, network)

	// Overall status is always healthy, RPC health is informational
	resp := HealthResponse{
		Status:    StatusHealthy,
		RPCHealth: rpcHealth,
	}

	if err := response.JSON(w, http.StatusOK, resp); err != nil {
		return httperror.InternalServerError("error writing health check response", err)
	}
	return nil
}

// checkRPCHealth checks the health of RPC service for a specific network
func (h *HealthHandler) checkRPCHealth(ctx context.Context, network string) types.GetHealthResponse {
	health, err := h.rpcService.GetHealth(ctx, network)
	if err != nil {
		return types.GetHealthResponse{
			Status: types.StatusError,
		}
	}
	return health
}
