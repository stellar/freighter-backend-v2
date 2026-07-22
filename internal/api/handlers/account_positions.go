// ABOUTME: Handler for GET /api/v1/accounts/{address}/positions — an account's
// ABOUTME: DeFi positions (Blend), served from the positions service.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/stellar/go/strkey"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const accountPositionsContextTimeout = 10 * time.Second

type AccountPositionsHandler struct {
	PositionsService types.PositionsService
}

func NewAccountPositionsHandler(positionsService types.PositionsService) *AccountPositionsHandler {
	return &AccountPositionsHandler{PositionsService: positionsService}
}

// GetAccountPositions handles GET /api/v1/accounts/{address}/positions.
// An account with no positions (including one unknown to the indexer)
// returns 200 with an empty positions list, not 404.
func (h *AccountPositionsHandler) GetAccountPositions(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), accountPositionsContextTimeout)
	defer cancel()

	network := r.URL.Query().Get("network")
	if !isValidWalletBackendNetwork(network) {
		return httperror.BadRequest(fmt.Sprintf("invalid network: must be %s or %s", types.PUBLIC, types.TESTNET), errors.New("invalid network"))
	}

	address := r.PathValue("address")
	if _, err := strkey.Decode(strkey.VersionByteAccountID, address); err != nil {
		return httperror.BadRequest(fmt.Sprintf("invalid Stellar address %s: %s", address, err.Error()), err)
	}

	positions, err := h.PositionsService.GetAccountPositions(ctx, address, network)
	if err != nil {
		return translateServiceError(r.Context(), err, "account positions", address, network)
	}

	return response.OK(w, HttpResponse{Data: positions})
}
