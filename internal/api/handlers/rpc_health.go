package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

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

	// Validate against the finite set of supported networks before passing the
	// value downstream. The value flows into Prometheus metric labels, so an
	// unbounded, caller-controlled network would create unbounded label
	// cardinality and unbounded memory growth.
	if !isValidNetwork(network) {
		return httperror.BadRequest(
			fmt.Sprintf("invalid network: network must be %s, %s or %s", types.PUBLIC, types.TESTNET, types.FUTURENET),
			errors.New("invalid network"),
		)
	}

	// Get RPC health status
	health, err := h.rpcService.GetHealth(r.Context(), network)
	if err != nil {
		// Return unhealthy status on error instead of failing the request
		resp := types.GetHealthResponse{
			Status: types.StatusUnhealthy,
		}
		if err := response.JSON(w, http.StatusOK, resp); err != nil {
			return httperror.InternalServerError("error writing RPC health check response", err)
		}
		return nil
	}

	resp := types.GetHealthResponse{
		Status: health.Status,
	}

	if err := response.JSON(w, http.StatusOK, resp); err != nil {
		return httperror.InternalServerError("error writing RPC health check response", err)
	}
	return nil
}
