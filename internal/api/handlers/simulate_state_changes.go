// ABOUTME: HTTP handler for POST /api/v1/simulate-state-changes, the preview of an unsubmitted transaction.
// ABOUTME: Validates the request and maps wallet-backend's "cannot preview" codes to 4xx responses.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/api/middleware"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

// SimulateStateChangesContextTimeout caps each preview request. A preview that
// arrives later than this is no longer useful to the sign view.
const SimulateStateChangesContextTimeout = 3 * time.Second

type SimulateStateChangesHandler struct {
	WalletBackendService types.WalletBackendService
}

func NewSimulateStateChangesHandler(walletBackendService types.WalletBackendService) *SimulateStateChangesHandler {
	return &SimulateStateChangesHandler{WalletBackendService: walletBackendService}
}

// SimulateStateChangesRequest carries the transaction to preview and the
// account the preview is for: the wallet account asked to sign. Only state
// changes on that account are returned. A muxed (M...) address is treated as
// its base account, which is where state changes are recorded.
type SimulateStateChangesRequest struct {
	TransactionXDR string `json:"transaction_xdr"`
	AccountAddress string `json:"account_address"`
}

// SimulateStateChanges returns the state changes an unsubmitted transaction
// would produce on one account. A transaction that cannot be previewed is a
// 4xx whose extras.code carries wallet-backend's code.
func (h *SimulateStateChangesHandler) SimulateStateChanges(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), SimulateStateChangesContextTimeout)
	defer cancel()

	network := r.URL.Query().Get("network")
	if !isValidWalletBackendNetwork(network) {
		return httperror.BadRequest(fmt.Sprintf("invalid network: must be %s or %s", types.PUBLIC, types.TESTNET), errors.New("invalid network"))
	}

	var req SimulateStateChangesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if middleware.IsMaxBytesError(err) {
			return httperror.RequestEntityTooLarge("Request body too large", err)
		}
		return httperror.BadRequest(fmt.Sprintf("invalid JSON: %s", err.Error()), err)
	}
	if req.TransactionXDR == "" {
		errStr := "transaction_xdr cannot be empty"
		return httperror.BadRequest(errStr, errors.New(errStr))
	}

	accountAddress, ok := baseAccountAddress(req.AccountAddress)
	if !ok {
		errStr := "account_address must be a valid account or contract address"
		return httperror.BadRequest(errStr, errors.New(errStr))
	}

	result, err := h.WalletBackendService.SimulateStateChanges(ctx, req.TransactionXDR, accountAddress, network)
	if err != nil {
		var rejected *types.SimulationRejectedError
		if errors.As(err, &rejected) {
			return simulationRejectedHTTPError(rejected)
		}
		return translateServiceError(r.Context(), err, "simulated state changes", "", network)
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: result})
}

// baseAccountAddress returns the address state changes are recorded under:
// G... and C... addresses as given, and a muxed M... address as its base G...
// account. It reports false for anything else.
func baseAccountAddress(address string) (string, bool) {
	if utils.IsValidAccount(address) {
		return address, true
	}
	var muxed xdr.MuxedAccount
	if err := muxed.SetAddress(address); err != nil {
		return "", false
	}
	return muxed.ToAccountId().Address(), true
}

// simulationRejectedHTTPError maps a transaction that cannot be previewed to
// 400 (the XDR does not decode) or 422 (it decodes but cannot be simulated).
func simulationRejectedHTTPError(rejected *types.SimulationRejectedError) *httperror.HttpError {
	status := http.StatusUnprocessableEntity
	if rejected.Code == types.SimulationCodeInvalidTransactionXDR {
		status = http.StatusBadRequest
	}
	return httperror.NewHttpError(rejected.Message, rejected, status, map[string]interface{}{"code": rejected.Code})
}
