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
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils/assetid"
)

type TokenPricesHandler struct {
	PricesService types.PricesService
	MaxTokens     int
}

func NewTokenPricesHandler(svc types.PricesService, maxTokens int) *TokenPricesHandler {
	return &TokenPricesHandler{PricesService: svc, MaxTokens: maxTokens}
}

type TokenPricesRequest struct {
	Tokens []string `json:"tokens"`
}

type validatedTokenPricesRequest struct {
	originalInputs      []string
	canonicalIDs        []string
	originalByCanonical map[string]string
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
	if maxTokens > 0 && len(req.Tokens) > maxTokens {
		errStr := fmt.Sprintf("too many tokens: maximum is %d, got %d", maxTokens, len(req.Tokens))
		return nil, httperror.BadRequest(errStr, errors.New(errStr))
	}

	canonicalIDs := make([]string, 0, len(req.Tokens))
	originalByCanonical := make(map[string]string, len(req.Tokens))
	for _, t := range req.Tokens {
		canonical, err := assetid.Normalize(t)
		if err != nil {
			return nil, httperror.BadRequest("invalid token id", err)
		}
		if _, dup := originalByCanonical[canonical]; !dup {
			canonicalIDs = append(canonicalIDs, canonical)
			originalByCanonical[canonical] = t
		}
	}

	return &validatedTokenPricesRequest{
		originalInputs:      req.Tokens,
		canonicalIDs:        canonicalIDs,
		originalByCanonical: originalByCanonical,
	}, nil
}

// GetPrices handles POST /api/v1/token-prices.
func (h *TokenPricesHandler) GetPrices(w http.ResponseWriter, r *http.Request) error {
	network := r.URL.Query().Get("network")
	if !isValidNetwork(network) {
		return httperror.BadRequest(fmt.Sprintf("invalid network: network must be %s, %s or %s", types.PUBLIC, types.TESTNET, types.FUTURENET), errors.New("invalid network"))
	}
	if network == types.FUTURENET {
		return httperror.BadRequest("token prices are not available on FUTURENET", errors.New("futurenet not supported"))
	}

	req, validationErr := validateTokenPricesRequest(r, h.MaxTokens)
	if validationErr != nil {
		return validationErr
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
	// echo behavior (so a request for "native" returns "native": ...).
	out := make(map[string]*types.PriceEntry, len(req.originalInputs))
	for _, original := range req.originalInputs {
		canonical, _ := assetid.Normalize(original)
		out[original] = prices[canonical]
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: out})
}
