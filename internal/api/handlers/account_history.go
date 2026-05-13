// ABOUTME: HTTP handlers for the account-history endpoints (transactions / operations / state-changes).
// ABOUTME: Shares parseRequest and translateServiceError helpers, all going through wallet-backend's GraphQL API.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/stellar/go/strkey"
	"github.com/stellar/wallet-backend/pkg/wbclient"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// translateServiceError maps a service-layer error to a typed HttpError per
// the spec's REST-honest mapping. Logs all non-404 errors with context;
// account-not-found is a normal client outcome and is not logged.
//
// Status mapping:
//   - wbclient.ErrAccountNotFound       -> 404
//   - context.DeadlineExceeded/Canceled -> 504
//   - *metrics.UpstreamError (any Kind) -> 502 (graphql_error, http_error)
//   - *url.Error / *net.OpError         -> 502 (transport / DNS / dial)
//   - anything else                     -> 500
func translateServiceError(ctx context.Context, err error, resource, address, network string) *httperror.HttpError {
	switch {
	case errors.Is(err, wbclient.ErrAccountNotFound):
		return httperror.NotFound(fmt.Sprintf("%s not found", resource), err)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		logger.ErrorWithContext(ctx, "wallet-backend call timed out", "resource", resource, "address", address, "network", network, "error", err)
		return httperror.GatewayTimeout(fmt.Sprintf("Failed to get %s", resource), err)
	}
	var upErr *metrics.UpstreamError
	if errors.As(err, &upErr) {
		logger.ErrorWithContext(ctx, "wallet-backend upstream error", "resource", resource, "address", address, "network", network, "kind", upErr.Kind, "code", upErr.Code, "error", err)
		return httperror.BadGateway(fmt.Sprintf("Failed to get %s", resource), err)
	}
	// Transport-layer failures come back from the SDK unwrapped (the SDK's
	// classifyWBError patterns only match GraphQL / HTTP-status strings).
	// Map them to 502 as the spec requires.
	var urlErr *url.Error
	var netErr *net.OpError
	if errors.As(err, &urlErr) || errors.As(err, &netErr) {
		logger.ErrorWithContext(ctx, "wallet-backend transport error", "resource", resource, "address", address, "network", network, "error", err)
		return httperror.BadGateway(fmt.Sprintf("Failed to get %s", resource), err)
	}
	logger.ErrorWithContext(ctx, "wallet-backend call failed", "resource", resource, "address", address, "network", network, "error", err)
	return httperror.InternalServerError(fmt.Sprintf("Failed to get %s", resource), err)
}

// AccountHistoryContextTimeout caps each /accounts/{address}/{...} request
// at 10s. Matches AccountBalancesContextTimeout.
const AccountHistoryContextTimeout = 10 * time.Second

// AccountHistoryHandler serves the three account-scoped paginated history
// endpoints. All three methods route through parseRequest and translateServiceError.
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
		if n < 1 || n > h.MaxLimit {
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
