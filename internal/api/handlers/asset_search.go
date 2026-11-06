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
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/services"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// AssetSearchContextTimeout caps one /assets/search request, like the other
// StellarExpert-backed routes. The upstream call runs detached, so when this
// expires the caller gets a 503 while the fetch still fills the cache.
const AssetSearchContextTimeout = 9 * time.Second

// AssetSearchHandler serves GET /api/v1/assets/search.
type AssetSearchHandler struct {
	Service types.AssetSearchService
}

// NewAssetSearchHandler returns a handler backed by svc.
func NewAssetSearchHandler(svc types.AssetSearchService) *AssetSearchHandler {
	return &AssetSearchHandler{Service: svc}
}

// SearchAssets handles GET /api/v1/assets/search: StellarExpert's /asset list,
// called with our API key and cached, for wallets that can no longer call it
// anonymously. `network` (PUBLIC or TESTNET) is ours; every other parameter is
// StellarExpert's own (search, sort, order, limit, cursor,
// includeUninitialized). The response is {"data": <StellarExpert's body>}.
func (h *AssetSearchHandler) SearchAssets(w http.ResponseWriter, r *http.Request) error {
	params := r.URL.Query()
	network := params.Get("network")
	if network != types.PUBLIC && network != types.TESTNET {
		return httperror.BadRequest(fmt.Sprintf("invalid network: network must be %s or %s", types.PUBLIC, types.TESTNET), errors.New("invalid network"))
	}
	params.Del("network")

	ctx, cancel := context.WithTimeout(r.Context(), AssetSearchContextTimeout)
	defer cancel()

	body, err := h.Service.SearchAssets(ctx, network, params)
	if err != nil {
		return assetSearchHTTPError(r.Context(), err)
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: body})
}

// assetSearchHTTPError maps a search failure to a status. Wallets fall back
// to held tokens on any non-2xx, so the codes are for operators: 400 is the
// caller's query, 500 is our credentials or a bug, and 503 is an upstream
// blip that heals by itself.
func assetSearchHTTPError(ctx context.Context, err error) *httperror.HttpError {
	// Order matters: the two upstream sentinels arrive wrapped in an
	// UpstreamError, so they are matched before the transient catch-all.
	switch {
	case errors.Is(err, services.ErrInvalidAssetSearchParams):
		return httperror.BadRequest(err.Error(), err)
	case errors.Is(err, services.ErrAssetMalformed):
		return httperror.BadRequest("StellarExpert rejected the query", err)
	case errors.Is(err, services.ErrUpstreamAuth):
		// Our key was rejected or our balance is spent. That fails every
		// request until someone changes config, so it must stay loud;
		// FreighterBackendV2StellarExpertCredentialsRejected pages on it.
		logger.ErrorWithContext(ctx, "asset search: stellar expert rejected our credentials", "error", err)
		return httperror.InternalServerError("Failed to search assets", err)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return httperror.ServiceUnavailable("asset search temporarily unavailable", err)
	}

	var upErr *metrics.UpstreamError
	if errors.As(err, &upErr) {
		logger.Warn("asset search: stellar expert unavailable", "error", err)
		return httperror.ServiceUnavailable("asset search temporarily unavailable", err)
	}

	logger.ErrorWithContext(ctx, "asset search failed", "error", err)
	return httperror.InternalServerError("Failed to search assets", err)
}
