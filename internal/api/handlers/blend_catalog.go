// ABOUTME: Handlers for the Blend market-catalog endpoints:
// ABOUTME: GET /protocols/blend/pools and GET /protocols/blend/earn-options.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const blendCatalogContextTimeout = 10 * time.Second

type BlendCatalogHandler struct {
	CatalogService types.BlendCatalogService
}

func NewBlendCatalogHandler(catalogService types.BlendCatalogService) *BlendCatalogHandler {
	return &BlendCatalogHandler{CatalogService: catalogService}
}

// GetPools handles GET /api/v1/protocols/blend/pools: the pool-wide market
// catalog, independent of any account.
func (h *BlendCatalogHandler) GetPools(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), blendCatalogContextTimeout)
	defer cancel()

	network := r.URL.Query().Get("network")
	if !isValidWalletBackendNetwork(network) {
		return httperror.BadRequest(fmt.Sprintf("invalid network: must be %s or %s", types.PUBLIC, types.TESTNET), errors.New("invalid network"))
	}

	pools, err := h.CatalogService.GetPools(ctx, network)
	if err != nil {
		return translateServiceError(r.Context(), err, "blend pools", "", network)
	}
	return response.OK(w, HttpResponse{Data: pools})
}

// GetEarnOptions handles GET /api/v1/protocols/blend/earn-options: the
// asset-first "where can I earn this" catalog, allowlist-curated.
func (h *BlendCatalogHandler) GetEarnOptions(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), blendCatalogContextTimeout)
	defer cancel()

	network := r.URL.Query().Get("network")
	if !isValidWalletBackendNetwork(network) {
		return httperror.BadRequest(fmt.Sprintf("invalid network: must be %s or %s", types.PUBLIC, types.TESTNET), errors.New("invalid network"))
	}

	options, err := h.CatalogService.GetEarnOptions(ctx, network)
	if err != nil {
		return translateServiceError(r.Context(), err, "blend earn options", "", network)
	}
	return response.OK(w, HttpResponse{Data: options})
}
