package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	HealthCheckContextTimeout = 5 * time.Second
)

// HealthResponse struct ensures the service status map is always present.
// The omitempty tag is removed for service_status if it should always be present.
type HealthResponse struct {
	ServiceStatus map[string]string `json:"service_status"` // Removed omitempty
}

type HealthHandler struct {
	services []types.Service
}

func NewHealthHandler(services ...types.Service) *HealthHandler {
	return &HealthHandler{
		services: services,
	}
}

// CheckHealth handles health check requests, including RPC service health.
func (h *HealthHandler) CheckHealth(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), HealthCheckContextTimeout)
	defer cancel()

	serviceStatus := make(map[string]string)
	overallHealthy := true

	for _, service := range h.services {
		serviceName := service.Name()
		response, err := service.GetHealth(ctx)
		if err != nil {
			errStr := fmt.Sprintf("health check for service '%s' failed: %v", serviceName, err)
			logger.ErrorWithContext(ctx, errStr)
			overallHealthy = false
		}

		if response.Status != types.StatusHealthy {
			overallHealthy = false
		}
		serviceStatus[serviceName] = response.Status
	}

	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	resp := HealthResponse{
		ServiceStatus: serviceStatus,
	}

	if !overallHealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		errStr := fmt.Sprintf("error writing health check response body: %v", err)
		logger.ErrorWithContext(ctx, errStr)
		return httperror.NewHttpError(errStr, err, http.StatusInternalServerError, nil)
	}
	return nil
}
