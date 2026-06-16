package handlers

import (
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
)

const (
	StatusHealthy = "healthy"
)

// HealthResponse contains the health status of the service.
type HealthResponse struct {
	Status string `json:"status"`
}

type HealthHandler struct{}

func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// CheckHealth handles health check requests. This is a dependency-free liveness
// signal: it reports only that the process is up and serving HTTP. Dependency
// health (DB, RPC) is reported by dedicated endpoints so that a downstream
// outage never causes this liveness check to restart or depool the pod.
func (h *HealthHandler) CheckHealth(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	resp := HealthResponse{
		Status: StatusHealthy,
	}

	if err := response.JSON(w, http.StatusOK, resp); err != nil {
		return httperror.InternalServerError("error writing health check response", err)
	}
	return nil
}
