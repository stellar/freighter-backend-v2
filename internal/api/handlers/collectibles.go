package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/go/txnbuild"
)

var (
	ErrInvalidBody = httperror.ErrorMessage{
		LogMessage:    "invalid request body %s: %v",
		ClientMessage: "An error occurred while fetching collectibles.",
	}
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

type ContractDetails struct {
	ID       string   `json:"id"`
	TokenIDs []string `json:"token_ids"`
}

type CollectibleRequest struct {
	Owner     string
	Contracts []ContractDetails `json:"contracts"`
}

type CollectibleResponse map[string]map[string]utils.Collectible

type GetCollectiblesPayload struct {
	Collectibles CollectibleResponse `json:"collectibles"`
}

type CollectiblesHandler struct {
	RpcService types.RPCService
}

func DecodeCollectibleRequest(r *http.Request) (*CollectibleRequest, error) {
	var req CollectibleRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	check := func(value any, name string) error {
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("missing or empty key: %s", name)
			}
		case []ContractDetails:
			if len(v) == 0 {
				return fmt.Errorf("missing or empty key: %s", name)
			}
		case []string:
			if len(v) == 0 {
				return fmt.Errorf("missing or empty key: %s", name)
			}
		}
		return nil
	}

	if err := check(req.Owner, "owner"); err != nil {
		return nil, err
	}
	if err := check(req.Contracts, "contracts"); err != nil {
		return nil, err
	}

	for i, c := range req.Contracts {
		if err := check(c.ID, fmt.Sprintf("contracts[%d].id", i)); err != nil {
			return nil, err
		}
		if err := check(c.TokenIDs, fmt.Sprintf("contracts[%d].token_ids", i)); err != nil {
			return nil, err
		}
	}

	return &req, nil
}

func NewCollectiblesHandler(rpc types.RPCService) *CollectiblesHandler {
	return &CollectiblesHandler{
		RpcService: rpc,
	}
}

func (h *CollectiblesHandler) GetCollectibles(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), HealthCheckContextTimeout)
	defer cancel()

	req, err := DecodeCollectibleRequest(r)
	if err != nil {
		logger.Error("GetCollectibles: invalid request", "err", err)
		return httperror.BadRequest(ErrInvalidBody.ClientMessage+err.Error(), err)
	}

	owner := strings.TrimSpace(req.Owner)
	if !utils.IsValidStellarPublicKey(owner) {
		err := errors.New("owner is not a valid stellar public key")
		logger.Error("GetCollectibles: invalid owner", "owner", owner, "err", err)
		return httperror.InternalServerError(ErrBadContractId.ClientMessage, err)
	}

	accountId := &txnbuild.SimpleAccount{
		AccountID: owner,
	}

	results := make(CollectibleResponse)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan error, 1)

	for _, contract := range req.Contracts {
		if !utils.IsValidContractID(strings.TrimSpace(contract.ID)) {
			logger.Error("GetCollectibles: invalid contract address", "contractId", contract.ID)
			return httperror.InternalServerError(ErrBadContractId.ClientMessage, errors.New("invalid contract ID"))
		}

		// Initialize inner map for this contract if nil
		if results[contract.ID] == nil {
			results[contract.ID] = make(map[string]utils.Collectible)
		}

		for _, tokenId := range contract.TokenIDs {
			wg.Add(1)
			go func(contractID, tokenID string) {
				defer wg.Done()
				collectible, err := utils.FetchCollectible(h.RpcService, ctx, accountId, contractID, tokenID)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				mu.Lock()
				results[contract.ID][tokenID] = *collectible
				mu.Unlock()
			}(contract.ID, tokenId)
		}
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case err := <-errCh:
		logger.Error("GetCollectibles: RPC error", "err", err)
		return httperror.InternalServerError("Failed to fetch collectibles", err)
	case <-ctx.Done():
		return httperror.InternalServerError("Timeout fetching collectibles", ctx.Err())
	}

	responseData := HttpResponse{
		Data: GetCollectiblesPayload{Collectibles: results},
	}

	if err := response.OK(w, responseData); err != nil {
		logger.ErrorWithContext(ctx, fmt.Sprintf("Failed to encode response: %v", err))
		return httperror.InternalServerError("Failed to encode response", err)
	}
	return nil
}
