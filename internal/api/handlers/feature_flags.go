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
	CoinbaseEnabled bool `json:"coinbase_enabled"`
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
		CoinbaseEnabled: true,
	}
	if platform == "ios" {
		resp.SwapEnabled = false
		resp.DiscoverEnabled = false
		resp.CoinbaseEnabled = false
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(resp)
}
