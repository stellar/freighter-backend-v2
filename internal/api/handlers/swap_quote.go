package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	xoxno "github.com/xoxno/sdk-go"
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/api/middleware"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/freighter-backend-v2/internal/utils/assetid"
)

const (
	// SwapContextTimeout bounds the swap routes; it must stay under the server
	// write timeout.
	SwapContextTimeout = 9 * time.Second

	defaultSwapTxTimeout   = 180
	maxSwapTxTimeout       = 900
	minSwapSlippagePercent = 0.0001
	maxSwapSlippagePercent = 50
)

// SwapQuoteHandler serves the swap quote route.
type SwapQuoteHandler struct {
	SwapQuoteService types.SwapQuoteService
}

// NewSwapQuoteHandler returns a handler that quotes swaps through svc.
func NewSwapQuoteHandler(svc types.SwapQuoteService) *SwapQuoteHandler {
	return &SwapQuoteHandler{SwapQuoteService: svc}
}

type swapQuoteBody struct {
	SourceAsset string `json:"sourceAsset"`
	DestAsset   string `json:"destAsset"`
	// Exactly one of SourceAmount (the user typed what to sell) and DestAmount
	// (the user typed what to receive) is set.
	SourceAmount string `json:"sourceAmount"`
	DestAmount   string `json:"destAmount"`
	// SourceDecimals and DestDecimals default to 7, the precision of every
	// classic asset. Required to be set for a Soroban contract token of any
	// other precision.
	SourceDecimals  *int    `json:"sourceDecimals"`
	DestDecimals    *int    `json:"destDecimals"`
	Sender          string  `json:"sender"`
	SlippagePercent float64 `json:"slippagePercent"`
	TimeoutSeconds  int64   `json:"timeoutSeconds"`
}

// validateSwapQuoteRequest decodes and validates a swap quote request body.
func validateSwapQuoteRequest(r *http.Request, network string) (*types.SwapQuoteRequest, *httperror.HttpError) {
	body, herr := decodeSwapQuoteBody(r)
	if herr != nil {
		return nil, herr
	}
	if !utils.IsValidStellarPublicKey(body.Sender) {
		return nil, httperror.BadRequest("sender must be a Stellar account address", errors.New("invalid sender"))
	}
	srcAsset, dstAsset, herr := validateSwapAssets(body)
	if herr != nil {
		return nil, herr
	}
	if body.SlippagePercent < minSwapSlippagePercent || body.SlippagePercent > maxSwapSlippagePercent {
		return nil, httperror.BadRequestf("slippagePercent must be between %v and %d", minSwapSlippagePercent, maxSwapSlippagePercent)
	}
	srcDecimals, dstDecimals, herr := validateSwapAmount(body, srcAsset, dstAsset)
	if herr != nil {
		return nil, herr
	}
	timeout, herr := validateSwapTimeout(body.TimeoutSeconds)
	if herr != nil {
		return nil, herr
	}

	return &types.SwapQuoteRequest{
		Network:         network,
		SourceAsset:     srcAsset,
		DestAsset:       dstAsset,
		SourceAmount:    body.SourceAmount,
		SourceDecimals:  srcDecimals,
		DestAmount:      body.DestAmount,
		DestDecimals:    dstDecimals,
		Sender:          body.Sender,
		SlippagePercent: body.SlippagePercent,
		TimeoutSeconds:  timeout,
	}, nil
}

// decodeSwapQuoteBody reads the JSON request body.
func decodeSwapQuoteBody(r *http.Request) (swapQuoteBody, *httperror.HttpError) {
	var body swapQuoteBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if middleware.IsMaxBytesError(err) {
			return body, httperror.RequestEntityTooLarge("Request body too large", err)
		}
		return body, httperror.BadRequest(fmt.Sprintf("invalid JSON: %s", err.Error()), err)
	}
	return body, nil
}

// validateSwapAssets returns the canonical source and destination assets,
// which must be valid and differ.
func validateSwapAssets(body swapQuoteBody) (src, dst string, herr *httperror.HttpError) {
	src, err := canonicalSwapAsset(body.SourceAsset)
	if err != nil {
		return "", "", httperror.BadRequest(fmt.Sprintf("invalid sourceAsset: %s", err), err)
	}
	dst, err = canonicalSwapAsset(body.DestAsset)
	if err != nil {
		return "", "", httperror.BadRequest(fmt.Sprintf("invalid destAsset: %s", err), err)
	}
	if src == dst {
		return "", "", httperror.BadRequestf("sourceAsset and destAsset must differ")
	}
	return src, dst, nil
}

// validateSwapAmount requires exactly one parseable amount and returns both
// assets' decimals.
func validateSwapAmount(body swapQuoteBody, srcAsset, dstAsset string) (srcDecimals, dstDecimals int, herr *httperror.HttpError) {
	if (body.SourceAmount == "") == (body.DestAmount == "") {
		return 0, 0, httperror.BadRequestf("exactly one of sourceAmount and destAmount is required")
	}
	srcDecimals, err := swapDecimals(body.SourceDecimals, srcAsset, "sourceDecimals")
	if err != nil {
		return 0, 0, httperror.BadRequest(err.Error(), err)
	}
	dstDecimals, err = swapDecimals(body.DestDecimals, dstAsset, "destDecimals")
	if err != nil {
		return 0, 0, httperror.BadRequest(err.Error(), err)
	}
	field, amount, decimals := "sourceAmount", body.SourceAmount, srcDecimals
	if body.DestAmount != "" {
		field, amount, decimals = "destAmount", body.DestAmount, dstDecimals
	}
	if _, err := xoxno.ParseDecimalAmount(amount, decimals); err != nil {
		return 0, 0, httperror.BadRequest(fmt.Sprintf("invalid %s: %s", field, err), err)
	}
	return srcDecimals, dstDecimals, nil
}

// validateSwapTimeout returns the transaction timeout in seconds, defaulted
// when unset.
func validateSwapTimeout(seconds int64) (int64, *httperror.HttpError) {
	if seconds == 0 {
		return defaultSwapTxTimeout, nil
	}
	if seconds < 0 || seconds > maxSwapTxTimeout {
		return 0, httperror.BadRequestf("timeoutSeconds must be between 1 and %d", maxSwapTxTimeout)
	}
	return seconds, nil
}

// swapDecimals returns an asset's decimals: the request's value for a Soroban
// contract token, which must give it because it cannot be read off the id, and the
// fixed classic precision for any other asset.
func swapDecimals(requested *int, asset, field string) (int, error) {
	if !utils.IsValidContractID(asset) {
		if requested != nil && *requested != types.ClassicDecimals {
			return 0, fmt.Errorf("%s must be %d for a classic asset", field, types.ClassicDecimals)
		}
		return types.ClassicDecimals, nil
	}
	if requested == nil {
		return 0, fmt.Errorf("%s is required for a Soroban token", field)
	}
	if *requested < 0 || *requested > xoxno.MaxAmountDecimals {
		return 0, fmt.Errorf("%s must be between 0 and %d", field, xoxno.MaxAmountDecimals)
	}
	return *requested, nil
}

// canonicalSwapAsset returns the canonical form of a classic asset id or a
// Soroban contract id.
func canonicalSwapAsset(id string) (string, error) {
	if utils.IsValidContractID(id) {
		return id, nil
	}
	return assetid.Normalize(id)
}

// GetSwapQuote returns the best swap route across every configured source.
func (h *SwapQuoteHandler) GetSwapQuote(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), SwapContextTimeout)
	defer cancel()

	network := r.URL.Query().Get("network")
	if !isValidWalletBackendNetwork(network) {
		return httperror.BadRequest(fmt.Sprintf("invalid network: must be %s or %s", types.PUBLIC, types.TESTNET), errors.New("invalid network"))
	}

	req, validationErr := validateSwapQuoteRequest(r, network)
	if validationErr != nil {
		return validationErr
	}

	quote, err := h.SwapQuoteService.GetBestQuote(ctx, *req)
	if err != nil {
		return translateServiceError(r.Context(), err, "swap quote", "", network)
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: quote})
}
