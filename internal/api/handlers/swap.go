package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"

	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/api/middleware"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/services/swap"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	// swapMaxDecimals bounds the amount of a non-classic asset; the source
	// rechecks against the token's real decimals.
	swapMaxDecimals = 18
	// maxInt64Stroops caps classic amounts: a classic balance cannot exceed it.
	maxInt64Stroops = "9223372036854775807"
)

type SwapHandler struct {
	svc types.SwapService
}

func NewSwapHandler(svc types.SwapService) *SwapHandler {
	return &SwapHandler{svc: svc}
}

func maxInt64StroopsBig() *big.Int {
	v, _ := new(big.Int).SetString(maxInt64Stroops, 10)
	return v
}

func swapBadRequest(msg string) *httperror.HttpError {
	return httperror.BadRequest(msg, errors.New(msg))
}

func decodeSwapBody(r *http.Request, dst any) *httperror.HttpError {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		if middleware.IsMaxBytesError(err) {
			return httperror.RequestEntityTooLarge("Request body too large", err)
		}
		return httperror.BadRequest("invalid request body", err)
	}
	return nil
}

func unitsFor(a swap.Asset) int {
	if a.IsClassic() {
		return swap.ClassicDecimals
	}
	return swapMaxDecimals
}

// validateSwapCommon checks the fields shared by both routes. Fixed messages
// only: ParseAsset errors echo user input.
func validateSwapCommon(network, account, from, to, amount string) *httperror.HttpError {
	if !isValidNetwork(network) {
		return swapBadRequest("invalid network")
	}
	if !strkey.IsValidEd25519PublicKey(account) {
		return swapBadRequest("invalid account")
	}
	fromAsset, err := swap.ParseAsset(from)
	if err != nil {
		return swapBadRequest("invalid fromAsset")
	}
	toAsset, err := swap.ParseAsset(to)
	if err != nil {
		return swapBadRequest("invalid toAsset")
	}
	// Compare contract IDs, not strings: "XLM" and the native contract ID are
	// the same asset.
	fromID, err := fromAsset.ContractID(network)
	if err != nil {
		return swapBadRequest("invalid fromAsset")
	}
	toID, err := toAsset.ContractID(network)
	if err != nil {
		return swapBadRequest("invalid toAsset")
	}
	if fromID == toID {
		return swapBadRequest("fromAsset and toAsset must differ")
	}
	units, err := swap.ParseUnits(amount, unitsFor(fromAsset))
	if err != nil {
		return swapBadRequest("invalid amount")
	}
	if fromAsset.IsClassic() {
		if units.Cmp(maxInt64StroopsBig()) > 0 {
			return swapBadRequest("invalid amount")
		}
	}
	return nil
}

// Quotes handles POST /api/v1/swap/quotes.
func (h *SwapHandler) Quotes(w http.ResponseWriter, r *http.Request) error {
	var req types.SwapQuoteRequest
	if herr := decodeSwapBody(r, &req); herr != nil {
		return herr
	}
	req.Network = r.URL.Query().Get("network")
	if herr := validateSwapCommon(req.Network, req.Account, req.FromAsset, req.ToAsset, req.Amount); herr != nil {
		return herr
	}
	if req.Mode != types.SwapModeSend && req.Mode != types.SwapModeReceive {
		return swapBadRequest("invalid mode")
	}

	out, err := h.svc.Quote(r.Context(), req)
	if err != nil {
		logger.ErrorWithContext(r.Context(), "swap quote failed", "network", req.Network, "error", err)
		return mapSwapError(err, false)
	}
	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: out})
}

// Build handles POST /api/v1/swap/tx.
func (h *SwapHandler) Build(w http.ResponseWriter, r *http.Request) error {
	var req types.SwapBuildRequest
	if herr := decodeSwapBody(r, &req); herr != nil {
		return herr
	}
	req.Network = r.URL.Query().Get("network")
	if herr := validateSwapCommon(req.Network, req.Account, req.FromAsset, req.ToAsset, req.Amount); herr != nil {
		return herr
	}
	if req.Source == "" {
		return swapBadRequest("invalid source")
	}
	// validateSwapCommon already parsed both assets.
	toAsset, _ := swap.ParseAsset(req.ToAsset)
	if _, err := swap.ParseUnits(req.MinOut, unitsFor(toAsset)); err != nil {
		return swapBadRequest("invalid minOut")
	}
	fee, err := swap.ParseUnits(req.Fee, swap.ClassicDecimals)
	if err != nil || fee.Cmp(maxInt64StroopsBig()) > 0 {
		return swapBadRequest("invalid fee")
	}

	if req.Slippage != "" {
		if _, err := swap.SlippageFraction(req.Slippage); err != nil {
			return swapBadRequest("invalid slippage")
		}
	}
	if req.Timeout != nil && (*req.Timeout < 1 || *req.Timeout > swap.MaxTimeoutSeconds) {
		return swapBadRequest("invalid timeout")
	}

	out, err := h.svc.Build(r.Context(), req)
	if err != nil {
		logger.ErrorWithContext(r.Context(), "swap build failed", "account", req.Account, "source", req.Source, "error", err)
		return mapSwapError(err, true)
	}
	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: out})
}

func mapSwapError(err error, build bool) *httperror.HttpError {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return httperror.ServiceUnavailable("swap temporarily unavailable", err)
	case errors.Is(err, swap.ErrAllSourcesFailed):
		return httperror.BadGateway("swap sources unavailable", err)
	case errors.Is(err, swap.ErrAccountNotFound):
		return httperror.BadRequest("account not found", err)
	case errors.Is(err, swap.ErrBadRequest):
		return httperror.BadRequest("invalid swap request", err)
	case errors.Is(err, swap.ErrCheckFailed):
		return httperror.BadGateway("swap transaction check failed", err)
	case errors.Is(err, swap.ErrLifiMissingTx):
		return httperror.BadGateway("swap source returned no transaction", err)
	case errors.Is(err, swap.ErrQuoteMoved):
		return httperror.Conflict("quote_moved", err)
	case build && (errors.Is(err, swap.ErrNoRoute) || errors.Is(err, swap.ErrInsufficientBalance)):
		return httperror.Conflict("quote_moved", err)
	case errors.As(err, new(*metrics.UpstreamError)), errors.Is(err, swap.ErrRateLimited):
		return httperror.BadGateway("swap source unavailable", err)
	}
	return httperror.InternalServerError("swap failed", err)
}
