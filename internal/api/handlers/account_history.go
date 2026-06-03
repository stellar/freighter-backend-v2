// ABOUTME: HTTP handler for the account-transactions history endpoint (transactions with embedded ops + state changes).
// ABOUTME: Validates the request via parseRequest and maps service errors via translateServiceError, all through wallet-backend's GraphQL API.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/stellar/go/strkey"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// AccountHistoryContextTimeout caps each /accounts/{address}/transactions request
// at 10s. Matches AccountBalancesContextTimeout.
const AccountHistoryContextTimeout = 10 * time.Second

// AccountHistoryHandler serves the single account-scoped transactions history
// endpoint. GetAccountTransactions routes through parseRequest and translateServiceError.
type AccountHistoryHandler struct {
	WalletBackendService types.WalletBackendService
	DefaultLimit         int
	MaxLimit             int
}

// NewAccountHistoryHandler returns a handler configured with the given page
// size bounds. Returns an error on invalid inputs so cmd/serve fails fast at
// startup, matching NewWalletBackendService's pattern.
func NewAccountHistoryHandler(svc types.WalletBackendService, defaultLimit, maxLimit int) (*AccountHistoryHandler, error) {
	if defaultLimit <= 0 {
		return nil, fmt.Errorf("account-history default limit must be > 0, got %d", defaultLimit)
	}
	if maxLimit <= 0 {
		return nil, fmt.Errorf("account-history max limit must be > 0, got %d", maxLimit)
	}
	if maxLimit > math.MaxInt32 {
		return nil, fmt.Errorf("account-history max limit must be <= %d, got %d", math.MaxInt32, maxLimit)
	}
	if defaultLimit > maxLimit {
		return nil, fmt.Errorf("account-history default limit (%d) must be <= max limit (%d)", defaultLimit, maxLimit)
	}
	return &AccountHistoryHandler{
		WalletBackendService: svc,
		DefaultLimit:         defaultLimit,
		MaxLimit:             maxLimit,
	}, nil
}

// parseRequest extracts and validates the address path variable, the network
// query param, and the cursor/pagination/time-range query params, returning
// the AccountHistoryParams ready to hand to the service. Returns *httperror.HttpError
// (always status 400) on any validation failure.
func (h *AccountHistoryHandler) parseRequest(r *http.Request) (address, network string, p types.AccountHistoryParams, herr *httperror.HttpError) {
	address = r.PathValue("address")
	if _, err := strkey.Decode(strkey.VersionByteAccountID, address); err != nil {
		return "", "", types.AccountHistoryParams{}, httperror.BadRequest(fmt.Sprintf("invalid Stellar address %s: %s", address, err.Error()), err)
	}

	network = r.URL.Query().Get("network")
	if !isValidWalletBackendNetwork(network) {
		return "", "", types.AccountHistoryParams{}, httperror.BadRequest(fmt.Sprintf("invalid network: must be %s or %s", types.PUBLIC, types.TESTNET), errors.New("invalid network"))
	}

	limit := h.DefaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return "", "", types.AccountHistoryParams{}, httperror.BadRequest(fmt.Sprintf("invalid limit %q: not an integer", s), err)
		}
		if n < 1 || n > h.MaxLimit || n > math.MaxInt32 {
			return "", "", types.AccountHistoryParams{}, httperror.BadRequest(fmt.Sprintf("invalid limit %d: must be between 1 and %d", n, h.MaxLimit), errors.New("limit out of range"))
		}
		limit = n
	}
	p.Limit = int32(limit)

	switch d := r.URL.Query().Get("direction"); d {
	case "", string(types.PaginationDirectionNext):
		p.Direction = types.PaginationDirectionNext
	case string(types.PaginationDirectionPrev):
		p.Direction = types.PaginationDirectionPrev
	default:
		return "", "", types.AccountHistoryParams{}, httperror.BadRequest(fmt.Sprintf("invalid direction %q: must be %q or %q", d, types.PaginationDirectionNext, types.PaginationDirectionPrev), errors.New("invalid direction"))
	}

	if c := r.URL.Query().Get("cursor"); c != "" {
		p.Cursor = &c
	}

	if s := r.URL.Query().Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return "", "", types.AccountHistoryParams{}, httperror.BadRequest(fmt.Sprintf("invalid since %q: must be RFC3339", s), err)
		}
		p.Since = &t
	}
	if u := r.URL.Query().Get("until"); u != "" {
		t, err := time.Parse(time.RFC3339, u)
		if err != nil {
			return "", "", types.AccountHistoryParams{}, httperror.BadRequest(fmt.Sprintf("invalid until %q: must be RFC3339", u), err)
		}
		p.Until = &t
	}
	if p.Since != nil && p.Until != nil && p.Since.After(*p.Until) {
		return "", "", types.AccountHistoryParams{}, httperror.BadRequest("since must be before until", errors.New("since after until"))
	}

	return address, network, p, nil
}

// GetAccountTransactions returns one page of an account's transactions from
// wallet-backend, in the spec's PaginatedResponse[T] envelope.
func (h *AccountHistoryHandler) GetAccountTransactions(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), AccountHistoryContextTimeout)
	defer cancel()

	address, network, params, herr := h.parseRequest(r)
	if herr != nil {
		return herr
	}

	result, err := h.WalletBackendService.GetAccountTransactions(ctx, address, network, params)
	if err != nil {
		return translateServiceError(r.Context(), err, "account transactions", address, network)
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, result)
}
