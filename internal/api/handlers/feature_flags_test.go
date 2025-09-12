package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeatureFlagsHandler(t *testing.T) {
	handler := NewFeatureFlagsHandler()

	tests := []struct {
		name             string
		platform         string
		expectedResponse FeatureFlagsResponse
	}{
		{
			name:     "android platform enables both flags",
			platform: "android",
			expectedResponse: FeatureFlagsResponse{
				SwapEnabled:     true,
				DiscoverEnabled: true,
			},
		},
		{
			name:     "ios platform disables both flags",
			platform: "ios",
			expectedResponse: FeatureFlagsResponse{
				SwapEnabled:     false,
				DiscoverEnabled: false,
			},
		},
		{
			name:     "no platform query param (defaults to enabled)",
			platform: "",
			expectedResponse: FeatureFlagsResponse{
				SwapEnabled:     true,
				DiscoverEnabled: true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/feature-flags?platform="+tc.platform, nil)
			rr := httptest.NewRecorder()

			err := handler.GetFeatureFlags(rr, req)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, rr.Code)

			var resp FeatureFlagsResponse
			err = json.NewDecoder(rr.Body).Decode(&resp)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedResponse, resp)
		})
	}
}
