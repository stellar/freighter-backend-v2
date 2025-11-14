package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alitto/pond/v2"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/go/txnbuild"
)

const (
	MaxConcurrentRPCCalls      = 10
	CollectiblesContextTimeout = 30 * time.Second
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

type TokenError struct {
	TokenID      string `json:"token_id"`
	ErrorMessage string `json:"error_message"`
}

type CollectionError struct {
	ErrorMessage      string       `json:"error_message"`
	CollectionAddress string       `json:"collection_address,omitempty"`
	Tokens            []TokenError `json:"tokens,omitempty"`
}

type CollectionResult struct {
	Collection *Collection      `json:"collection,omitempty"`
	Error      *CollectionError `json:"error,omitempty"`
}

type CollectibleResponse []CollectionResult

type GetCollectiblesPayload struct {
	Collections CollectibleResponse `json:"collections"`
}

type CollectiblesHandler struct {
	RpcService                     types.RPCService
	MeridianPayTreasureHuntAddress string
	MeridianPayTreasurePoapAddress string
	maxConcurrentRPCCalls          int
	pool                           pond.Pool
}

func NewCollectiblesHandler(rpc types.RPCService, meridianPayTreasureHuntAddress string, meridianPayTreasurePoapAddress string, maxConcurrentRPCCalls int) *CollectiblesHandler {
	return &CollectiblesHandler{
		RpcService:                     rpc,
		MeridianPayTreasureHuntAddress: meridianPayTreasureHuntAddress,
		MeridianPayTreasurePoapAddress: meridianPayTreasurePoapAddress,
		maxConcurrentRPCCalls:          maxConcurrentRPCCalls,
		pool:                           pond.NewPool(maxConcurrentRPCCalls),
	}
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
	network string,
) (*Collection, *CollectionError) {

	if !utils.IsValidContractID(c.ID) {
		return nil, &CollectionError{
			ErrorMessage:      fmt.Sprintf("invalid contract ID: %s", c.ID),
			CollectionAddress: c.ID,
		}
	}

	details, err := FetchCollection(h.RpcService, ctx, account, c.ID, network, h.pool)
	if err != nil {
		return nil, &CollectionError{
			ErrorMessage:      fmt.Sprintf("fetching collection: %v", err),
			CollectionAddress: c.ID,
		}
	}

	collectibles, tokenErrs := h.fetchCollectibles(ctx, account, c.ID, c.TokenIDs, network)

	if len(collectibles) == 0 {
		// If no collectibles were fetched (either no tokens requested or all fetches failed), treat as collection-level failure
		return nil, &CollectionError{
			ErrorMessage:      fmt.Sprintf("no collectibles fetched for contract %s", c.ID),
			CollectionAddress: c.ID,
			Tokens:            tokenErrs,
		}
	}

	var colErr *CollectionError
	if len(tokenErrs) > 0 {
		colErr = &CollectionError{
			CollectionAddress: c.ID,
			Tokens:            tokenErrs,
		}
	}

	return &Collection{
		CollectionAddress: c.ID,
		Name:              details.Name,
		Symbol:            details.Symbol,
		Collectibles:      collectibles,
	}, colErr
}

func (h *CollectiblesHandler) fetchCollectibles(
	ctx context.Context,
	account *txnbuild.SimpleAccount,
	contractID string,
	tokenIDs []string,
	network string,
) ([]Collectible, []TokenError) {

	var (
		results   []Collectible
		tokenErrs []TokenError
		mu        sync.Mutex
	)

	group := h.pool.NewGroupContext(ctx)

	for _, tokenID := range tokenIDs {
		tokenID := tokenID // Capture loop variable
		group.Submit(func() {
			c, err := fetchCollectible(h.RpcService, ctx, account, contractID, tokenID, network, h.pool)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				tokenErrs = append(tokenErrs, TokenError{
					TokenID:      tokenID,
					ErrorMessage: err.Error(),
				})
				return
			}
			results = append(results, *c)
		})
	}

	_ = group.Wait() // Task errors already captured in tokenErrs

	return results, tokenErrs
}

func (h *CollectiblesHandler) fetchMeridianPayCollectibles(
	ctx context.Context,
	account *txnbuild.SimpleAccount,
	owner string,
	network string,
) ([]CollectionResult, error) {
	contracts := []string{}
	if h.MeridianPayTreasureHuntAddress != "" {
		contracts = append(contracts, h.MeridianPayTreasureHuntAddress)
	}
	if h.MeridianPayTreasurePoapAddress != "" {
		contracts = append(contracts, h.MeridianPayTreasurePoapAddress)
	}

	if len(contracts) == 0 {
		return []CollectionResult{}, nil
	}

	results := make([]CollectionResult, len(contracts))

	group := h.pool.NewGroupContext(ctx)

	for i, contract := range contracts {
		i, contract := i, contract // Capture loop variables
		group.Submit(func() {

			tokenIds, err := fetchOwnerTokens(h.RpcService, ctx, account, contract, owner, network)
			if err != nil {
				results[i] = CollectionResult{
					Error: &CollectionError{
						ErrorMessage:      fmt.Sprintf("fetching owner tokens: %v", err),
						CollectionAddress: contract,
					},
				}
				return
			}

			contractDetails := contractDetails{
				ID:       contract,
				TokenIDs: tokenIds,
			}

			collection, colErr := h.fetchCollection(ctx, account, contractDetails, network)
			results[i] = CollectionResult{
				Collection: collection,
				Error:      colErr,
			}
		})
	}

	if err := group.Wait(); err != nil {
		return results, fmt.Errorf("waiting for meridian pay collectibles: %w", err)
	}

	return results, nil
}

func (h *CollectiblesHandler) GetCollectibles(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), CollectiblesContextTimeout)
	defer cancel()

	network := r.URL.Query().Get("network")
	if network != types.PUBLIC && network != types.TESTNET && network != types.FUTURENET {
		// After clients have updated to use the network query param, we can remove this and return the error
		network = types.PUBLIC
		// return httperror.BadRequest(fmt.Sprintf("invalid network: network must be %s, %s or %s", types.PUBLIC, types.TESTNET, types.FUTURENET), errors.New("invalid network"))
	}

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
	skipContracts := mapset.NewSet[string]()
	if h.MeridianPayTreasureHuntAddress != "" {
		skipContracts.Add(h.MeridianPayTreasureHuntAddress)
	}
	if h.MeridianPayTreasurePoapAddress != "" {
		skipContracts.Add(h.MeridianPayTreasurePoapAddress)
	}

	// Filter user-requested contracts to exclude Meridian Pay addresses
	var filteredContracts []contractDetails
	for _, c := range req.Contracts {
		if !skipContracts.Contains(c.ID) {
			filteredContracts = append(filteredContracts, c)
		}
	}

	results := make([]CollectionResult, len(filteredContracts))

	group := h.pool.NewGroupContext(ctx)
	for i, contract := range filteredContracts {
		i, contract := i, contract // Capture loop variables
		group.Submit(func() {
			collection, colErr := h.fetchCollection(ctx, account, contract, network)
			if colErr != nil && len(colErr.Tokens) == 0 && collection == nil {
				results[i] = CollectionResult{Error: colErr}
				return
			}
			results[i] = CollectionResult{
				Collection: collection,
				Error:      colErr,
			}
		})
	}
	_ = group.Wait() // Wait for all contracts to complete or context to cancel

	meridianResults, err := h.fetchMeridianPayCollectibles(ctx, account, owner, network)
	if err != nil {
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrInternal.LogMessage, err))
	}
	allResults := append(results, meridianResults...)

	responseData := HttpResponse{
		Data: GetCollectiblesPayload{
			Collections: allResults,
		},
	}
	return response.OK(w, responseData)
}
