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
	ErrInternal = httperror.ErrorMessage{
		LogMessage:    "internal failure %s: %v",
		ClientMessage: "An error occurred while fetching collectibles.",
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

type Collection struct {
	CollectionAddress string              `json:"address"`
	Name              string              `json:"name"`
	Symbol            string              `json:"symbol"`
	Collectibles      []utils.Collectible `json:"collectibles"`
}

type CollectibleResponse []Collection

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
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrInvalidBody.LogMessage, err))
		return httperror.BadRequest(ErrInvalidBody.ClientMessage+err.Error(), err)
	}

	owner := strings.TrimSpace(req.Owner)
	if !utils.IsValidStellarPublicKey(owner) {
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrBadContractId.LogMessage, err))
		return httperror.InternalServerError(ErrBadContractId.ClientMessage, errors.New("invalid owner"))
	}

	accountId := &txnbuild.SimpleAccount{AccountID: owner}
	results := make([]Collection, 0, len(req.Contracts))
	var mu sync.Mutex
	errCh := make(chan error, 1)

	for _, contract := range req.Contracts {
		if !utils.IsValidContractID(strings.TrimSpace(contract.ID)) {
			logger.ErrorWithContext(ctx, fmt.Sprintf(ErrBadContractId.LogMessage, errors.New("invalid contract ID")))
			return httperror.InternalServerError(ErrBadContractId.ClientMessage, errors.New("invalid contract ID"))
		}

		collectionDetails, err := utils.FetchCollection(h.RpcService, ctx, accountId, contract.ID)
		if err != nil {
			logger.ErrorWithContext(ctx, fmt.Sprintf(ErrInternal.LogMessage, err))
			return httperror.InternalServerError(ErrInternal.ClientMessage, err)
		}

		collection := Collection{
			CollectionAddress: contract.ID,
			Name:              collectionDetails.Name,
			Symbol:            collectionDetails.Symbol,
			Collectibles:      make([]utils.Collectible, 0, len(contract.TokenIDs)),
		}

		var wg sync.WaitGroup

		for _, tokenID := range contract.TokenIDs {
			wg.Add(1)
			go func(contractID, tokenID string) {
				defer wg.Done()
				collectible, err := utils.FetchCollectible(h.RpcService, ctx, accountId, contractID, tokenID, http.DefaultClient)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				mu.Lock()
				collection.Collectibles = append(collection.Collectibles, *collectible)
				mu.Unlock()
			}(contract.ID, tokenID)
		}

		wg.Wait()

		mu.Lock()
		results = append(results, collection)
		mu.Unlock()
	}

	select {
	case err := <-errCh:
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrInternal.LogMessage, err))
		return httperror.InternalServerError("Failed to fetch collectibles", err)
	default:
	}

	responseData := HttpResponse{
		Data: GetCollectiblesPayload{Collectibles: results},
	}

	return response.OK(w, responseData)
}
