package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

var (
	ErrBadContractId = httperror.ErrorMessage{
		LogMessage:    "invalid contract id %s: %v",
		ClientMessage: "An error occurred while fetching collectibles.",
	}
	ErrFailedSimulation = httperror.ErrorMessage{
		LogMessage:    "simulation failure %s: %v",
		ClientMessage: "An error occurred while fetching collectibles.",
	}
	ErrFailedToEncodeCollectiblesToJSONResponse = httperror.ErrorMessage{
		LogMessage:    "failed to encode collectibles to JSON response: %v",
		ClientMessage: "An error occurred while formatting the response.",
	}
)

type Collectible struct {
	Balance  string      `json:"balances"`
	Metadata interface{} `json:"metadata"`
}

type GetCollectiblesPayload struct {
	Collectibles []Collectible `json:"collectibles"`
}

type CollectiblesHandler struct {
	RpcService types.RPCService
}

func NewCollectiblesHandler(rpc types.RPCService) *CollectiblesHandler {
	return &CollectiblesHandler{
		RpcService: rpc,
	}
}

func (h *CollectiblesHandler) GetCollectibles(w http.ResponseWriter, r *http.Request) error {
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	contracts := r.URL.Query()["contract"]

	if !utils.IsValidStellarPublicKey(owner) {
		return httperror.InternalServerError(ErrBadContractId.ClientMessage, errors.New("owner is not a valid stellar public key"))
	}

	ctx, cancel := context.WithTimeout(r.Context(), HealthCheckContextTimeout)
	defer cancel()

	accountId := &txnbuild.SimpleAccount{
		AccountID: owner,
	}
	ownerAddress, err := utils.ScAddressFromString(owner)
	if err != nil {
		return httperror.InternalServerError(ErrBadContractId.ClientMessage, err)
	}

	scVal := xdr.ScVal{
		Type:    xdr.ScValTypeScvAddress,
		Address: ownerAddress,
	}
	timeout := txnbuild.NewTimeout(300)

	var results []Collectible
	for _, contractId := range contracts {
		if !utils.IsValidContractID(strings.TrimSpace(contractId)) {
			return httperror.InternalServerError(ErrBadContractId.ClientMessage, errors.New("invalid contract ID"))
		}

		id, err := utils.ScAddressFromString(contractId)
		if err != nil {
			return httperror.InternalServerError(ErrBadContractId.ClientMessage, err)
		}

		balance, err := h.RpcService.InvokeContract(ctx, *id, accountId, "balance", []xdr.ScVal{scVal}, timeout)
		if err != nil {
			return httperror.InternalServerError(ErrFailedSimulation.ClientMessage, err)
		}

		metadata, err := h.RpcService.InvokeContract(ctx, *id, accountId, "get_metadata", []xdr.ScVal{}, timeout)
		if err != nil {
			return httperror.InternalServerError(ErrFailedSimulation.ClientMessage, err)
		}

		results = append(results, Collectible{
			Balance:  balance.String(),
			Metadata: metadata,
		})
	}

	responseData := HttpResponse{
		Data: GetCollectiblesPayload{
			Collectibles: results,
		},
	}

	if err := response.OK(w, responseData); err != nil {
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrFailedToEncodeCollectiblesToJSONResponse.LogMessage, err))
		return httperror.InternalServerError(ErrFailedToEncodeCollectiblesToJSONResponse.ClientMessage, err)
	}
	return nil
}
