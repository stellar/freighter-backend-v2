package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/stellar/go/strkey"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/api/middleware"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	AccountBalancesContextTimeout = 10 * time.Second
)

type AccountBalancesHandler struct {
	WalletBackendService types.WalletBackendService
}

func NewAccountBalancesHandler(walletBackendService types.WalletBackendService) *AccountBalancesHandler {
	return &AccountBalancesHandler{
		WalletBackendService: walletBackendService,
	}
}

type AccountBalancesRequest struct {
	Addresses []string `json:"addresses"`
}

func validateAccountBalancesRequest(r *http.Request) (*AccountBalancesRequest, *httperror.HttpError) {
	var req AccountBalancesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if middleware.IsMaxBytesError(err) {
			return nil, httperror.RequestEntityTooLarge("Request body too large", err)
		}
		return nil, httperror.BadRequest(fmt.Sprintf("invalid JSON: %s", err.Error()), err)
	}

	if len(req.Addresses) == 0 {
		return nil, httperror.BadRequest("addresses array cannot be empty", errors.New("addresses array cannot be empty"))
	}

	// Validate each address is a valid Stellar address
	for _, addr := range req.Addresses {
		if _, err := strkey.Decode(strkey.VersionByteAccountID, addr); err != nil {
			return nil, httperror.BadRequest(fmt.Sprintf("invalid Stellar address %s: %s", addr, err.Error()), err)
		}
	}

	return &req, nil
}

// GetAccountBalances handles fetching account balances from wallet backend
func (h *AccountBalancesHandler) GetAccountBalances(w http.ResponseWriter, r *http.Request) error {
	contextWithTimeout, cancel := context.WithTimeout(r.Context(), AccountBalancesContextTimeout)
	defer cancel()

	queryParams := r.URL.Query()
	network := queryParams.Get("network")

	if !isValidNetwork(network) {
		return httperror.BadRequest(fmt.Sprintf("invalid network: network must be %s, %s or %s", types.PUBLIC, types.TESTNET, types.FUTURENET), errors.New("invalid network"))
	}

	req, validationErr := validateAccountBalancesRequest(r)
	if validationErr != nil {
		return validationErr
	}

	balances, err := h.WalletBackendService.GetBalancesByAccountAddresses(contextWithTimeout, req.Addresses, network)
	if err != nil {
		logger.ErrorWithContext(r.Context(), "getting account balances from wallet backend", "error", err)
		return httperror.InternalServerError("Failed to get account balances", err)
	}

	responseData := HttpResponse{
		Data: balances,
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, responseData)
}
