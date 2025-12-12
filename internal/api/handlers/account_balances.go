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

func validateAccountBalancesRequest(r *http.Request) (*AccountBalancesRequest, error) {
	var req AccountBalancesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	if len(req.Addresses) == 0 {
		return nil, errors.New("addresses array cannot be empty")
	}

	// Validate each address is a valid Stellar address
	for _, addr := range req.Addresses {
		if _, err := strkey.Decode(strkey.VersionByteAccountID, addr); err != nil {
			return nil, fmt.Errorf("invalid Stellar address %s: %w", addr, err)
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

	if network != types.PUBLIC && network != types.TESTNET {
		return httperror.BadRequest(fmt.Sprintf("invalid network: network must be %s or %s", types.PUBLIC, types.TESTNET), errors.New("invalid network"))
	}

	req, err := validateAccountBalancesRequest(r)
	if err != nil {
		return httperror.BadRequest(fmt.Sprintf("Invalid request: %s", err.Error()), err)
	}

	balances, err := h.WalletBackendService.GetBalancesByAccountAddresses(contextWithTimeout, req.Addresses, network)
	if err != nil {
		return httperror.InternalServerError(fmt.Sprintf("Failed to get account balances: %s", err.Error()), err)
	}

	responseData := HttpResponse{
		Data: balances,
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, responseData)
}
