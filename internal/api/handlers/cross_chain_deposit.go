// ABOUTME: HTTP handlers for cross-chain deposits: the chains list and the two deposit address routes.
// ABOUTME: Maps service errors to the feature's machine-readable codes, sent as `code` in the error extras.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/services"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

// CrossChainDepositContextTimeout is the deadline for each cross-chain
// deposit request.
const CrossChainDepositContextTimeout = 10 * time.Second

// CrossChainDepositHandler serves /api/v1/cross-chain-deposit.
type CrossChainDepositHandler struct {
	service *services.CrossChainDepositService
}

// NewCrossChainDepositHandler builds the handler.
func NewCrossChainDepositHandler(service *services.CrossChainDepositService) *CrossChainDepositHandler {
	return &CrossChainDepositHandler{service: service}
}

// crossChainChain is one chain in the chains response. Cross Mesh's
// contracts block is not exposed.
type crossChainChain struct {
	Chain                    string                    `json:"chain"`
	ChainID                  int64                     `json:"chain_id"`
	Status                   string                    `json:"status"`
	USDCAddress              string                    `json:"usdc_address"`
	Confirmations            int64                     `json:"confirmations"`
	MinSettlementAmount      string                    `json:"min_settlement_amount"`
	Decimals                 int                       `json:"decimals"`
	EstimatedDeliverySeconds *int64                    `json:"estimated_delivery_seconds"`
	Fees                     *types.CrossMeshChainFees `json:"fees"`
}

// GetChains handles GET /api/v1/cross-chain-deposit/chains?network=. It
// returns every chain with its status; the client shows the active ones.
func (h *CrossChainDepositHandler) GetChains(w http.ResponseWriter, r *http.Request) error {
	network := r.URL.Query().Get("network")
	if !isValidNetwork(network) {
		return crossChainError(http.StatusNotFound, "network_not_supported", "cross-chain deposits are not supported on this network", errors.New("invalid network"))
	}
	ctx, cancel := context.WithTimeout(r.Context(), CrossChainDepositContextTimeout)
	defer cancel()

	chains, err := h.service.GetChains(ctx, network)
	if err != nil {
		return crossChainHTTPError(ctx, err, network)
	}
	out := make([]crossChainChain, 0, len(chains.Chains))
	for _, c := range chains.Chains {
		out = append(out, crossChainChain{
			Chain:                    c.Chain,
			ChainID:                  c.ChainID,
			Status:                   c.Status,
			USDCAddress:              c.USDCAddress,
			Confirmations:            c.Confirmations,
			MinSettlementAmount:      c.MinSettlementAmount,
			Decimals:                 c.Decimals,
			EstimatedDeliverySeconds: c.Speeds.Standard.EstimatedDeliverySeconds,
			Fees:                     c.Fees,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: map[string]any{"chains": out}})
}

// CreateAddress handles POST /api/v1/cross-chain-deposit/addresses?network=
// with body {"account":"G…"}: get-or-create the account's deposit address.
func (h *CrossChainDepositHandler) CreateAddress(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Account string `json:"account"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return crossChainError(http.StatusBadRequest, "invalid_account", "request body must be {\"account\":\"G…\"}", err)
	}
	return h.address(w, r, body.Account, h.service.GetOrCreateAddress)
}

// LookupAddress handles GET
// /api/v1/cross-chain-deposit/accounts/{account}/address?network=: the
// account's existing deposit address, never creating one.
func (h *CrossChainDepositHandler) LookupAddress(w http.ResponseWriter, r *http.Request) error {
	return h.address(w, r, r.PathValue("account"), h.service.LookupAddress)
}

func (h *CrossChainDepositHandler) address(w http.ResponseWriter, r *http.Request, account string, get func(ctx context.Context, network, account string) (*services.DepositAddress, error)) error {
	if !utils.IsValidStellarPublicKey(account) {
		return crossChainError(http.StatusBadRequest, "invalid_account", "account must be a Stellar G address", errors.New("invalid account"))
	}
	network := r.URL.Query().Get("network")
	if !isValidNetwork(network) {
		return crossChainError(http.StatusNotFound, "network_not_supported", "cross-chain deposits are not supported on this network", errors.New("invalid network"))
	}
	ctx, cancel := context.WithTimeout(r.Context(), CrossChainDepositContextTimeout)
	defer cancel()

	addr, err := get(ctx, network, account)
	if err != nil {
		return crossChainHTTPError(ctx, err, network)
	}
	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: addr})
}

// crossChainError builds an error response whose extras carry the
// machine-readable code.
func crossChainError(status int, code, message string, err error) *httperror.HttpError {
	return httperror.NewHttpError(message, err, status, map[string]any{"code": code})
}

// crossChainHTTPError maps a service error to the feature's error codes.
// Cross Mesh's own code and request_id are added to the extras when known.
// Server-side failures (5xx) are logged.
func crossChainHTTPError(ctx context.Context, err error, network string) *httperror.HttpError {
	var cmErr *services.CrossMeshError
	isCrossMesh := errors.As(err, &cmErr)

	var herr *httperror.HttpError
	switch {
	case errors.Is(err, services.ErrCrossChainNotConfigured):
		herr = crossChainError(http.StatusNotFound, "network_not_supported", "cross-chain deposits are not supported on this network", err)
	case errors.Is(err, services.ErrNoDepositAddress):
		herr = crossChainError(http.StatusNotFound, "no_deposit_address", "this account has no deposit address", err)
	case errors.Is(err, services.ErrNoActiveChain):
		herr = crossChainError(http.StatusConflict, "no_active_chain", "no source chain is accepting deposits right now", err)
	case errors.Is(err, services.ErrAccountNotFound):
		herr = crossChainError(http.StatusUnprocessableEntity, "account_not_found", "account does not exist on the network", err)
	case errors.Is(err, services.ErrTrustlineMissing):
		herr = crossChainError(http.StatusUnprocessableEntity, "usdc_trustline_missing", "account needs a USDC trustline", err)
		env, _ := services.CrossMeshEnvFor(network)
		herr.Extras["asset_code"] = "USDC"
		herr.Extras["asset_issuer"] = env.USDCIssuer
	case errors.Is(err, services.ErrVerificationFailed):
		herr = crossChainError(http.StatusBadGateway, "verification_failed", "deposit address failed verification", err)
	case errors.Is(err, context.DeadlineExceeded):
		herr = crossChainError(http.StatusGatewayTimeout, "upstream_timeout", "upstream timed out", err)
	case errors.Is(err, services.ErrChainsUnavailable):
		herr = crossChainError(http.StatusBadGateway, "upstream_error", "chains are unavailable", err)
	case errors.Is(err, services.ErrTrustlineCheckFailed):
		herr = crossChainError(http.StatusServiceUnavailable, "upstream_unavailable", "Stellar RPC is unavailable", err)
	case isCrossMesh && cmErr.Code == "invalid_stellar_address":
		herr = crossChainError(http.StatusBadRequest, "invalid_account", "account must be a Stellar G address", err)
	case isCrossMesh && cmErr.Status >= http.StatusInternalServerError:
		herr = crossChainError(http.StatusServiceUnavailable, "upstream_unavailable", "upstream is unavailable", err)
	default:
		// Cross Mesh 4xx, an undecodable answer, or an unexpected row.
		herr = crossChainError(http.StatusBadGateway, "upstream_error", "upstream returned an unexpected answer", err)
	}

	if isCrossMesh {
		if cmErr.Code != "" {
			herr.Extras["upstream_code"] = cmErr.Code
		}
		if cmErr.RequestID != "" {
			herr.Extras["request_id"] = cmErr.RequestID
		}
	}
	if herr.StatusCode >= http.StatusInternalServerError {
		logger.ErrorWithContext(ctx, "cross-chain deposit request failed", "network", network, "code", herr.Extras["code"], "error", err)
	}
	return herr
}
