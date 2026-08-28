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
	"github.com/stellar/freighter-backend-v2/internal/services"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils/assetid"
)

// TokenPriceHistoryContextTimeout caps each /token-price-history and
// /token-stats request. It must stay strictly under api.DefaultWriteTimeout
// (10s): past that the server closes the connection mid-write and the client
// sees a dropped connection rather than the 503 the error mapping below
// produces. The service fans its upstream fetches out concurrently, so this
// is one fetch budget, not the sum of them.
const TokenPriceHistoryContextTimeout = 9 * time.Second

type TokenPriceHistoryHandler struct {
	HistoryService types.PriceHistoryService
}

func NewTokenPriceHistoryHandler(svc types.PriceHistoryService) *TokenPriceHistoryHandler {
	return &TokenPriceHistoryHandler{HistoryService: svc}
}

// tokenPriceHistoryResponse is the §6.1 wire payload: the client's original
// token string plus the service-computed history fields (flattened via
// embedding).
type tokenPriceHistoryResponse struct {
	Token string `json:"token"`
	types.TokenPriceHistory
}

// validateHistoryTokenAndNetwork parses the token and network query params
// shared by the history and stats endpoints. Identifiers are query params —
// not path segments — because canonical ids contain ':' and the extension's
// JWT signs pathname+search (B.3). Returns the canonical id (upstream form)
// and the original token string to echo back.
func validateHistoryTokenAndNetwork(r *http.Request, unavailable string) (canonical, original, network string, herr *httperror.HttpError) {
	q := r.URL.Query()

	network = q.Get("network")
	if !isValidNetwork(network) {
		return "", "", "", httperror.BadRequest(fmt.Sprintf("invalid network: network must be %s or %s", types.PUBLIC, types.TESTNET), errors.New("invalid network"))
	}
	if network == types.FUTURENET {
		return "", "", "", httperror.BadRequest(fmt.Sprintf("%s is not available on FUTURENET", unavailable), errors.New("futurenet not supported"))
	}

	original = q.Get("token")
	canonical, err := assetid.Normalize(original)
	if err != nil {
		return "", "", "", httperror.BadRequest("invalid token id", err)
	}
	return canonical, original, network, nil
}

// GetTokenPriceHistory handles GET /api/v1/token-price-history.
func (h *TokenPriceHistoryHandler) GetTokenPriceHistory(w http.ResponseWriter, r *http.Request) error {
	canonical, original, network, herr := validateHistoryTokenAndNetwork(r, "token price history")
	if herr != nil {
		return herr
	}

	historyRange := r.URL.Query().Get("range")
	if !services.IsValidPriceHistoryRange(historyRange) {
		return httperror.BadRequest("invalid range: range must be one of 1H, 1D, 1W, 1M, 1Y, ALL", errors.New("invalid range"))
	}

	ctx, cancel := context.WithTimeout(r.Context(), TokenPriceHistoryContextTimeout)
	defer cancel()

	history, err := h.HistoryService.GetPriceHistory(ctx, canonical, network, historyRange)
	if err != nil {
		logger.ErrorWithContext(r.Context(), "getting token price history", "error", err)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return httperror.ServiceUnavailable("token price history temporarily unavailable", err)
		}
		return httperror.InternalServerError("Failed to get token price history", err)
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: tokenPriceHistoryResponse{
		Token:             original,
		TokenPriceHistory: *history,
	}})
}
