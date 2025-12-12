package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
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
// When used with buffered response middleware, this allows safe error handling
// even after writing the response body.
func (h *HealthHandler) CheckHealth(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), HealthCheckContextTimeout)
	defer cancel()

	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	serviceStatus := make(map[string]string)
	overallHealthy := true
	network := r.URL.Query().Get("network")

	if network != types.PUBLIC && network != types.TESTNET && network != types.FUTURENET {
		return httperror.BadRequest(fmt.Sprintf("invalid network: network must be %s, %s or %s", types.PUBLIC, types.TESTNET, types.FUTURENET), errors.New("invalid network"))
	}

	type healthStatus struct {
		serviceName string
		response    types.GetHealthResponse
		error       error
	}

	healthCheckChan := make(chan healthStatus, len(h.services))
	for _, service := range h.services {
		go func(service types.Service) {
			response, err := service.GetHealth(ctx, network)

			healthCheckChan <- healthStatus{
				serviceName: service.Name(),
				response:    response,
				error:       err,
			}
		}(service)
	}

	for range h.services {
		result := <-healthCheckChan
		if result.error != nil {
			errStr := fmt.Sprintf("health check for service '%s' failed: %v", result.serviceName, result.error)
			logger.ErrorWithContext(ctx, errStr)
			overallHealthy = false
		}

		if result.response.Status != types.StatusHealthy {
			overallHealthy = false
		}
		serviceStatus[result.serviceName] = result.response.Status
	}

	resp := HealthResponse{
		ServiceStatus: serviceStatus,
	}

	// Determine status code based on health
	statusCode := http.StatusOK
	if !overallHealthy {
		statusCode = http.StatusServiceUnavailable
	}

	if err := response.JSON(w, statusCode, resp); err != nil {
		return httperror.InternalServerError("error writing health check response", err)
	}
	return nil
}
