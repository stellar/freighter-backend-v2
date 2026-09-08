package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/api/middleware"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils/assetid"
)

type TokenPricesHandler struct {
	PricesService types.PricesService
	MaxTokens     int
	// PricesMetrics may be nil (tests); counters become no-ops in that case.
	PricesMetrics *metrics.Prices
}

func NewTokenPricesHandler(svc types.PricesService, maxTokens int, pricesMetrics *metrics.Prices) *TokenPricesHandler {
	return &TokenPricesHandler{PricesService: svc, MaxTokens: maxTokens, PricesMetrics: pricesMetrics}
}

type TokenPricesRequest struct {
	Tokens []string `json:"tokens"`
}

type validatedTokenPricesRequest struct {
	originalInputs []string
	canonicalIDs   []string
	// canonicalByOriginal maps each raw client input to its canonical id so the
	// response loop can echo the original key without re-normalizing. Inputs
	// that failed to parse are absent from this map — they are skipped-and-
	// nulled in the response rather than failing the batch.
	canonicalByOriginal map[string]string
	// skipped counts inputs that failed to parse (LP-share ids, format
	// mistakes). Each appears in the response with a null price; the caller
	// bumps the per-skip metric with this count.
	skipped int
}

func validateTokenPricesRequest(r *http.Request, maxTokens int) (*validatedTokenPricesRequest, *httperror.HttpError) {
	var req TokenPricesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if middleware.IsMaxBytesError(err) {
			return nil, httperror.RequestEntityTooLarge("Request body too large", err)
		}
		return nil, httperror.BadRequest("invalid request body", err)
	}
	if len(req.Tokens) == 0 {
		errStr := "tokens array cannot be empty"
		return nil, httperror.BadRequest(errStr, errors.New(errStr))
	}

	canonicalIDs := make([]string, 0, len(req.Tokens))
	canonicalByOriginal := make(map[string]string, len(req.Tokens))
	seen := make(map[string]struct{}, len(req.Tokens))
	skipped := 0
	for _, t := range req.Tokens {
		canonical, err := assetid.Normalize(t)
		if err != nil {
			// Skip-and-null: one unparseable id (an LP-share id, a client
			// format mistake) degrades to one null entry, never a batch-wide
			// failure that would blank every price on the home screen.
			skipped++
			continue
		}
		canonicalByOriginal[t] = canonical
		if _, dup := seen[canonical]; !dup {
			seen[canonical] = struct{}{}
			canonicalIDs = append(canonicalIDs, canonical)
		}
	}
	if len(canonicalIDs) == 0 {
		errStr := "no parseable token ids in request"
		return nil, httperror.BadRequest(errStr, errors.New(errStr))
	}

	// Apply the cap on the deduped canonical set, not raw input — a request
	// with many duplicates collapses to a single upstream fetch and shouldn't
	// be rejected for shape reasons. Body-size middleware bounds the worst
	// case before we ever decode.
	if maxTokens > 0 && len(canonicalIDs) > maxTokens {
		errStr := fmt.Sprintf("too many tokens: maximum is %d, got %d unique", maxTokens, len(canonicalIDs))
		return nil, httperror.BadRequest(errStr, errors.New(errStr))
	}

	return &validatedTokenPricesRequest{
		originalInputs:      req.Tokens,
		canonicalIDs:        canonicalIDs,
		canonicalByOriginal: canonicalByOriginal,
		skipped:             skipped,
	}, nil
}

// GetPrices handles POST /api/v1/token-prices.
func (h *TokenPricesHandler) GetPrices(w http.ResponseWriter, r *http.Request) error {
	network := r.URL.Query().Get("network")
	if !isValidNetwork(network) {
		return httperror.BadRequest(fmt.Sprintf("invalid network: network must be %s or %s", types.PUBLIC, types.TESTNET), errors.New("invalid network"))
	}
	if network == types.FUTURENET {
		return httperror.BadRequest("token prices are not available on FUTURENET", errors.New("futurenet not supported"))
	}

	req, validationErr := validateTokenPricesRequest(r, h.MaxTokens)
	if validationErr != nil {
		return validationErr
	}
	if req.skipped > 0 && h.PricesMetrics != nil {
		h.PricesMetrics.SkippedTokens.WithLabelValues(network).Add(float64(req.skipped))
	}

	prices, err := h.PricesService.GetPrices(r.Context(), req.canonicalIDs, network)
	if err != nil {
		logger.ErrorWithContext(r.Context(), "getting token prices", "error", err)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return httperror.ServiceUnavailable("token prices temporarily unavailable", err)
		}
		return httperror.InternalServerError("Failed to get token prices", err)
	}

	// Build response keyed by the *original* client input, preserving v1's
	// echo behavior (so a request for "native" returns "native": ...). The
	// canonical id was already resolved during validation; skipped inputs are
	// absent from canonicalByOriginal and fall out as explicit nulls.
	out := make(map[string]*types.PriceEntry, len(req.originalInputs))
	for _, original := range req.originalInputs {
		if canonical, ok := req.canonicalByOriginal[original]; ok {
			out[original] = prices[canonical]
		} else {
			out[original] = nil
		}
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: out})
}
