// ABOUTME: Handler tests for the Blend catalog endpoints: validation, error
// ABOUTME: translation, and success envelopes for pools and earn options.
package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func serveCatalog(t *testing.T, svc types.BlendCatalogService, target string) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewBlendCatalogHandler(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/protocols/blend/pools", CustomHandler(handler.GetPools))
	mux.HandleFunc("GET /api/v1/protocols/blend/earn-options", CustomHandler(handler.GetEarnOptions))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestBlendCatalogSuccess(t *testing.T) {
	name := "Fixed Pool V2"
	svc := &utils.MockBlendCatalogService{
		GetPoolsResult: &types.BlendPoolsCatalog{
			Pools: []types.BlendCatalogPool{{ID: "CPOOL", Name: &name, Reserves: []types.BlendCatalogReserve{}}},
		},
		GetEarnOptionsResult: &types.BlendEarnOptionsCatalog{
			Options: []types.BlendEarnAssetOption{{AssetID: "CUSDC", Pools: []types.BlendEarnPool{{ID: "CPOOL"}}}},
		},
	}

	rec := serveCatalog(t, svc, "/api/v1/protocols/blend/pools?network=TESTNET")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"id":"CPOOL"`)
	assert.Contains(t, rec.Body.String(), `"name":"Fixed Pool V2"`)

	rec = serveCatalog(t, svc, "/api/v1/protocols/blend/earn-options?network=TESTNET")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"asset_id":"CUSDC"`)
}

func TestBlendCatalogValidation(t *testing.T) {
	svc := &utils.MockBlendCatalogService{}
	for _, target := range []string{
		"/api/v1/protocols/blend/pools",
		"/api/v1/protocols/blend/pools?network=NOPE",
		"/api/v1/protocols/blend/earn-options",
		"/api/v1/protocols/blend/earn-options?network=NOPE",
	} {
		rec := serveCatalog(t, svc, target)
		assert.Equal(t, http.StatusBadRequest, rec.Code, target)
	}
}

func TestBlendCatalogErrorTranslation(t *testing.T) {
	svc := &utils.MockBlendCatalogService{
		GetPoolsError:       &metrics.UpstreamError{Kind: "http_error", Code: 502, Err: errors.New("boom")},
		GetEarnOptionsError: errors.New("wat"),
	}

	rec := serveCatalog(t, svc, "/api/v1/protocols/blend/pools?network=TESTNET")
	assert.Equal(t, http.StatusBadGateway, rec.Code)

	rec = serveCatalog(t, svc, "/api/v1/protocols/blend/earn-options?network=TESTNET")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestBlendCatalogEmptyIs200(t *testing.T) {
	svc := &utils.MockBlendCatalogService{}

	rec := serveCatalog(t, svc, "/api/v1/protocols/blend/pools?network=TESTNET")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"pools":[]`)

	rec = serveCatalog(t, svc, "/api/v1/protocols/blend/earn-options?network=TESTNET")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"options":[]`)
}
