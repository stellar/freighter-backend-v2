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
	version := r.URL.Query().Get("version")

	resp := FeatureFlagsResponse{
		SwapEnabled:     true,
		DiscoverEnabled: true,
		OnrampEnabled:   true,
	}

	if platform == "ios" && version == "1.3.23" {
		resp.SwapEnabled = false
		resp.DiscoverEnabled = false
		resp.OnrampEnabled = false
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(resp)
}
