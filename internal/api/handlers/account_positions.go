// ABOUTME: Handler for POST /api/v1/accounts/positions — DeFi positions (Blend)
// ABOUTME: for a list of accounts, mirroring the balances endpoint's contract.
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

const accountPositionsContextTimeout = 10 * time.Second

type AccountPositionsHandler struct {
	PositionsService types.PositionsService
	MaxAddresses     int
}

func NewAccountPositionsHandler(positionsService types.PositionsService, maxAddresses int) *AccountPositionsHandler {
	return &AccountPositionsHandler{PositionsService: positionsService, MaxAddresses: maxAddresses}
}

// GetAccountsPositions handles POST /api/v1/accounts/positions. The request
// body is {"addresses": [...]}, identical to the balances endpoint. Accounts
// with no positions (including accounts unknown to the indexer) return empty
// positions inside a 200, not an error.
func (h *AccountPositionsHandler) GetAccountsPositions(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), accountPositionsContextTimeout)
	defer cancel()

	network := r.URL.Query().Get("network")
	if !isValidWalletBackendNetwork(network) {
		return httperror.BadRequest(fmt.Sprintf("invalid network: must be %s or %s", types.PUBLIC, types.TESTNET), errors.New("invalid network"))
	}

	req, validationErr := validateAccountBalancesRequest(r, h.MaxAddresses)
	if validationErr != nil {
		return validationErr
	}

	positions, err := h.PositionsService.GetAccountsPositions(ctx, req.Addresses, network)
	if err != nil {
		// address is intentionally empty: this is a multi-address fan-out and
		// a top-level error here is systemic (per-address "no positions"
		// outcomes are already normalized inside a 200).
		return translateServiceError(r.Context(), err, "account positions", "", network)
	}

	return response.OK(w, HttpResponse{Data: positions})
}
