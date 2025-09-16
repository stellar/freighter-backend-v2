package handlers

import (
	"context"
	"encoding/json"
	"net/http"
)

type FeatureFlagsHandler struct{}

type FeatureFlagsResponse struct {
	SwapEnabled     bool `json:"swap_enabled"`
	DiscoverEnabled bool `json:"discover_enabled"`
	OnrampEnabled   bool `json:"onramp_enabled"`
}

func NewFeatureFlagsHandler() *FeatureFlagsHandler {
	return &FeatureFlagsHandler{}
}

func (h *FeatureFlagsHandler) GetFeatureFlags(w http.ResponseWriter, r *http.Request) error {
	_, cancel := context.WithTimeout(r.Context(), HealthCheckContextTimeout)
	defer cancel()

	platform := r.URL.Query().Get("platform")

	resp := FeatureFlagsResponse{
		SwapEnabled:     true,
		DiscoverEnabled: true,
		OnrampEnabled:   true,
	}
	if platform == "ios" {
		resp.SwapEnabled = true
		resp.DiscoverEnabled = true
		resp.OnrampEnabled = true
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(resp)
}
