// ABOUTME: HTTP handlers for the account-history endpoints (transactions / operations / state-changes).
// ABOUTME: Shares parseRequest and translateServiceError helpers, all going through wallet-backend's GraphQL API.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"

	"github.com/stellar/wallet-backend/pkg/wbclient"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
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
