package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeatureFlagsHandler_GetFeatureFlags(t *testing.T) {
	t.Parallel()

	t.Run("should return swap_enabled=false for ios", func(t *testing.T) {
		t.Parallel()
		handler := NewFeatureFlagsHandler()

		req, _ := http.NewRequest("GET", "/api/v1/feature-flags?platform=ios", nil)
		rr := httptest.NewRecorder()

		err := handler.GetFeatureFlags(rr, req)
		require.NoError(t, err)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

		var resp FeatureFlagsResponse
		err = json.Unmarshal(rr.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.False(t, resp.SwapEnabled)
	})

	t.Run("should return swap_enabled=true for android", func(t *testing.T) {
		t.Parallel()
		handler := NewFeatureFlagsHandler()

		req, _ := http.NewRequest("GET", "/api/v1/feature-flags?platform=android", nil)
		rr := httptest.NewRecorder()

		err := handler.GetFeatureFlags(rr, req)
		require.NoError(t, err)

		assert.Equal(t, http.StatusOK, rr.Code)

		var resp FeatureFlagsResponse
		err = json.Unmarshal(rr.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.True(t, resp.SwapEnabled)
	})

	t.Run("should return swap_enabled=true when platform not provided", func(t *testing.T) {
		t.Parallel()
		handler := NewFeatureFlagsHandler()

		req, _ := http.NewRequest("GET", "/api/v1/feature-flags", nil)
		rr := httptest.NewRecorder()

		err := handler.GetFeatureFlags(rr, req)
		require.NoError(t, err)

		var resp FeatureFlagsResponse
		err = json.Unmarshal(rr.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.True(t, resp.SwapEnabled)
	})
}
