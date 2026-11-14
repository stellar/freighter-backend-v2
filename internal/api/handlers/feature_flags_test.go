package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/services"
)

func TestFeatureFlagsHandler(t *testing.T) {
	handler := NewFeatureFlagsHandler(nil)

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
			name:     "ios platform with disabled version 1.6.23",
			platform: "ios",
			version:  "1.6.23",
			expectedResponse: FeatureFlagsResponse{
				SwapEnabled:     false,
				DiscoverEnabled: false,
				OnrampEnabled:   false,
			},
		},
		{
			name:     "ios platform with newer version enables flags",
			platform: "ios",
			version:  "1.7.23",
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

// TR-2: the flag lists exactly the networks with a configured Cross Mesh
// environment; pubnet stays absent while its contracts are not pinned.
func TestFeatureFlags_TR2_ListsConfiguredNetworks(t *testing.T) {
	cases := map[string]struct {
		cfg  config.CrossMeshConfig
		want string
	}{
		"testnet key set": {config.CrossMeshConfig{TestnetAPIKey: "testnet-key", MainnetAPIKey: "mainnet-key"}, `["TESTNET"]`},
		"no keys":         {config.CrossMeshConfig{}, `[]`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			handler := NewFeatureFlagsHandler(services.CrossMeshConfiguredNetworks(tc.cfg))
			rr := httptest.NewRecorder()
			require.NoError(t, handler.GetFeatureFlags(rr, httptest.NewRequest(http.MethodGet, "/feature-flags", nil)))

			var resp map[string]json.RawMessage
			require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
			assert.JSONEq(t, tc.want, string(resp["cross_chain_deposit_networks"]))
		})
	}
}
