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
		version          string
		expectedResponse FeatureFlagsResponse
	}{
		{
			name:     "android platform enables all flags",
			platform: "android",
			version:  "",
			expectedResponse: FeatureFlagsResponse{
				SwapEnabled:     true,
				DiscoverEnabled: true,
				OnrampEnabled:   true,
			},
		},
		{
			name:     "ios platform with disabled version 1.3.23",
			platform: "ios",
			version:  "1.3.23",
			expectedResponse: FeatureFlagsResponse{
				SwapEnabled:     false,
				DiscoverEnabled: false,
				OnrampEnabled:   false,
			},
		},
		{
			name:     "ios platform with newer version enables flags",
			platform: "ios",
			version:  "1.3.24",
			expectedResponse: FeatureFlagsResponse{
				SwapEnabled:     true,
				DiscoverEnabled: true,
				OnrampEnabled:   true,
			},
		},
		{
			name:     "ios platform with no version defaults to enabled",
			platform: "ios",
			version:  "",
			expectedResponse: FeatureFlagsResponse{
				SwapEnabled:     true,
				DiscoverEnabled: true,
				OnrampEnabled:   true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url := "/feature-flags?platform=" + tc.platform
			if tc.version != "" {
				url += "&version=" + tc.version
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
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
