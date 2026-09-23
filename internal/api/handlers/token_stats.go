package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

type TokenStatsHandler struct {
	StatsService types.TokenStatsService
}

func NewTokenStatsHandler(svc types.TokenStatsService) *TokenStatsHandler {
	return &TokenStatsHandler{StatsService: svc}
}

// tokenStatsResponse echoes the client's original token string alongside the
// sourceable stats rows. Absent rows are omitted keys (D4) — the embedded
// struct's fields carry omitempty.
type tokenStatsResponse struct {
	Token string `json:"token"`
	types.TokenStats
}

// GetTokenStats handles GET /api/v1/token-stats.
func (h *TokenStatsHandler) GetTokenStats(w http.ResponseWriter, r *http.Request) error {
	canonical, original, network, herr := validateHistoryTokenAndNetwork(r, "token stats")
	if herr != nil {
		return herr
	}

	ctx, cancel := context.WithTimeout(r.Context(), TokenPriceHistoryContextTimeout)
	defer cancel()

	stats, err := h.StatsService.GetTokenStats(ctx, canonical, network)
	if err != nil {
		logger.ErrorWithContext(r.Context(), "getting token stats", "error", err)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return httperror.ServiceUnavailable("token stats temporarily unavailable", err)
		}
		return httperror.InternalServerError("Failed to get token stats", err)
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: tokenStatsResponse{
		Token:      original,
		TokenStats: *stats,
	}})
}
