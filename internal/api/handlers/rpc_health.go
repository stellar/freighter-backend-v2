package handlers

import (
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// RPCHealthResponse contains the health status of the RPC service.
type RPCHealthResponse struct {
	Status string `json:"status"`
}

type RPCHealthHandler struct {
	rpcService types.RPCService
}

func NewRPCHealthHandler(rpcService types.RPCService) *RPCHealthHandler {
	return &RPCHealthHandler{
		rpcService: rpcService,
	}
}

// CheckRPCHealth handles RPC health check requests.
func (h *RPCHealthHandler) CheckRPCHealth(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	// Get network from query parameter (default to PUBLIC)
	network := r.URL.Query().Get("network")
	if network == "" {
		network = types.PUBLIC
	}

	// Get RPC health status
	health, err := h.rpcService.GetHealth(r.Context(), network)
	if err != nil {
		// Return unhealthy status on error instead of failing the request
		resp := RPCHealthResponse{
			Status: types.StatusUnhealthy,
		}
		if err := response.JSON(w, http.StatusOK, resp); err != nil {
			return httperror.InternalServerError("error writing RPC health check response", err)
		}
		return nil
	}

	resp := RPCHealthResponse{
		Status: health.Status,
	}

	if err := response.JSON(w, http.StatusOK, resp); err != nil {
		return httperror.InternalServerError("error writing RPC health check response", err)
	}
	return nil
}
