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

type CollectibleResponse []Collection

type GetCollectiblesPayload struct {
	Collections CollectibleResponse `json:"collections"`
}

type CollectiblesHandler struct {
	RpcService types.RPCService
}

func validateRequest(r *http.Request) (*collectibleRequest, error) {
	var req collectibleRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	check := func(value any, name string) error {
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("missing or empty key: %s", name)
			}
		case []contractDetails:
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

func (h *CollectiblesHandler) fetchCollections(
	ctx context.Context,
	account *txnbuild.SimpleAccount,
	contracts []contractDetails,
) ([]Collection, error) {

	var (
		results []Collection
		mu      sync.Mutex
		wg      sync.WaitGroup
		errCh   = make(chan error, 1)
	)

	for _, contract := range contracts {
		wg.Add(1)
		go func(c contractDetails) {
			defer wg.Done()
			collection, err := h.fetchCollection(ctx, account, c)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}

			mu.Lock()
			results = append(results, *collection)
			mu.Unlock()
		}(contract)
	}

	wg.Wait()

	select {
	case err := <-errCh:
		return nil, err
	default:
		return results, nil
	}
}

func (h *CollectiblesHandler) fetchCollection(
	ctx context.Context,
	account *txnbuild.SimpleAccount,
	c contractDetails,
) (*Collection, error) {

	if !utils.IsValidContractID(strings.TrimSpace(c.ID)) {
		return nil, errors.New("invalid contract ID")
	}

	details, err := FetchCollection(h.RpcService, ctx, account, c.ID)
	if err != nil {
		return nil, err
	}

	collection := &Collection{
		CollectionAddress: c.ID,
		Name:              details.Name,
		Symbol:            details.Symbol,
		Collectibles:      make([]Collectible, 0, len(c.TokenIDs)),
	}

	collectibles, err := h.fetchCollectibles(ctx, account, c.ID, c.TokenIDs)
	if err != nil {
		return nil, err
	}

	collection.Collectibles = collectibles
	return collection, nil
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
		errCh   = make(chan error, 1)
	)

	for _, id := range tokenIDs {
		wg.Add(1)
		go func(tokenID string) {
			defer wg.Done()
			c, err := FetchCollectible(h.RpcService, ctx, account, contractID, tokenID)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}

			mu.Lock()
			results = append(results, *c)
			mu.Unlock()
		}(id)
	}

	wg.Wait()

	select {
	case err := <-errCh:
		return nil, err
	default:
		return results, nil
	}
}

func NewCollectiblesHandler(rpc types.RPCService) *CollectiblesHandler {
	return &CollectiblesHandler{
		RpcService: rpc,
	}
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

	account := &txnbuild.SimpleAccount{AccountID: owner}
	results, err := h.fetchCollections(ctx, account, req.Contracts)
	if err != nil {
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrInternal.LogMessage, err))
		return httperror.InternalServerError("Failed to fetch collectibles", err)
	}

	responseData := HttpResponse{
		Data: GetCollectiblesPayload{Collections: results},
	}

	return response.OK(w, responseData)
}
