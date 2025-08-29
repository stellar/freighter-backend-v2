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
		LogMessage:    "invalid request body: %v",
		ClientMessage: "An error occurred while fetching collectibles.",
	}
	ErrInternal = httperror.ErrorMessage{
		LogMessage:    "internal failure %s: %v",
		ClientMessage: "An error occurred while fetching collectibles.",
	}
)

type contractDetails struct {
	ID       string   `json:"id"`
	TokenIDs []string `json:"token_ids"`
}

type collectibleRequest struct {
	Owner     string
	Contracts []contractDetails `json:"contracts"`
}

type Collection struct {
	CollectionAddress string        `json:"address"`
	Name              string        `json:"name"`
	Symbol            string        `json:"symbol"`
	Collectibles      []Collectible `json:"collectibles"`
}

type CollectionResult struct {
	Collection *Collection `json:"collection,omitempty"`
	Error      string      `json:"error,omitempty"`
}

type CollectibleResponse []CollectionResult

type GetCollectiblesPayload struct {
	Collections CollectibleResponse `json:"collections"`
}

type CollectiblesHandler struct {
	RpcService types.RPCService
}

func NewCollectiblesHandler(rpc types.RPCService) *CollectiblesHandler {
	return &CollectiblesHandler{RpcService: rpc}
}

func validateRequest(r *http.Request) (*collectibleRequest, error) {
	var req collectibleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	req.Owner = strings.TrimSpace(req.Owner)
	if req.Owner == "" {
		return nil, errors.New("missing or empty owner")
	}

	if len(req.Contracts) == 0 {
		return nil, errors.New("missing or empty contracts")
	}

	for i, c := range req.Contracts {
		c.ID = strings.TrimSpace(c.ID)
		if c.ID == "" {
			return nil, fmt.Errorf("contracts[%d].id is empty", i)
		}
		if len(c.TokenIDs) == 0 {
			return nil, fmt.Errorf("contracts[%d].token_ids is empty", i)
		}
	}

	return &req, nil
}

func (h *CollectiblesHandler) fetchCollection(
	ctx context.Context,
	account *txnbuild.SimpleAccount,
	c contractDetails,
) (*Collection, error) {

	if !utils.IsValidContractID(c.ID) {
		return nil, fmt.Errorf("invalid contract ID: %s", c.ID)
	}

	details, err := FetchCollection(h.RpcService, ctx, account, c.ID)
	if err != nil {
		return nil, fmt.Errorf("fetching collection %s: %w", c.ID, err)
	}

	collectibles, err := h.fetchCollectibles(ctx, account, c.ID, c.TokenIDs)
	if err != nil {
		return nil, fmt.Errorf("fetching collectibles for %s: %w", c.ID, err)
	}

	// If no collectibles found, treat as an error
	if len(collectibles) == 0 {
		return nil, fmt.Errorf("no collectibles found for contract %s", c.ID)
	}

	return &Collection{
		CollectionAddress: c.ID,
		Name:              details.Name,
		Symbol:            details.Symbol,
		Collectibles:      collectibles,
	}, nil
}

func (h *CollectiblesHandler) fetchCollectibles(
	ctx context.Context,
	account *txnbuild.SimpleAccount,
	contractID string,
	tokenIDs []string,
) ([]Collectible, error) {

	var (
		results []Collectible
		mu      sync.Mutex
		wg      sync.WaitGroup
		errs    []error
	)

	for _, tokenID := range tokenIDs {
		wg.Add(1)
		go func(tokenID string) {
			defer wg.Done()
			c, err := FetchCollectible(h.RpcService, ctx, account, contractID, tokenID)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("fetching collectibles for contract: %s, token: %s: %w, err, contractID, tokenID", contractID, tokenID, err))
				mu.Unlock()
				return
			}
			mu.Lock()
			results = append(results, *c)
			mu.Unlock()
		}(tokenID)
	}

	wg.Wait()
	return results, nil
}

func (h *CollectiblesHandler) GetCollectibles(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), HealthCheckContextTimeout)
	defer cancel()

	req, err := validateRequest(r)
	if err != nil {
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrInvalidBody.LogMessage, err))
		return httperror.BadRequest(
			fmt.Sprintf("%s: %s", ErrInvalidBody.ClientMessage, err.Error()),
			err,
		)
	}

	owner := strings.TrimSpace(req.Owner)
	if !utils.IsValidAccount(owner) {
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrInvalidBody.LogMessage, err))
		return httperror.BadRequest(ErrInvalidBody.ClientMessage, errors.New("invalid owner"))
	}

	account := &txnbuild.SimpleAccount{AccountID: req.Owner}
	results := make([]CollectionResult, len(req.Contracts))
	var wg sync.WaitGroup

	for i, contract := range req.Contracts {
		wg.Add(1)
		go func(i int, c contractDetails) {
			defer wg.Done()
			collection, err := h.fetchCollection(ctx, account, c)
			if err != nil {
				logger.ErrorWithContext(ctx, fmt.Sprintf(ErrInternal.LogMessage, err))
				results[i] = CollectionResult{Error: err.Error()}
			} else {
				results[i] = CollectionResult{Collection: collection}
			}
		}(i, contract)
	}
	wg.Wait()

	responseData := HttpResponse{Data: GetCollectiblesPayload{Collections: results}}
	return response.OK(w, responseData)
}
