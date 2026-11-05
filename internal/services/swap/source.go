package swap

import (
	"context"
	"errors"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// Source is one liquidity venue that can quote a swap and build its
// transaction. checkSourceContract (source_contract_test.go) checks the route
// shape, ctx cancellation and the transaction's account and expiry; the
// MinOut floor, error mapping and third-party checks need the source's own
// tests. README.md lists what else a new source needs.
type Source interface {
	// Name is the route's "source" field, the /swap/tx "source" a client sends
	// back, and the metrics label, so it must never change.
	Name() string
	// Supports reports whether the source serves a request: its network, mode
	// and asset kinds. The router does not call Quote or BuildTx for one it
	// does not, so Quote and BuildTx may assume it.
	Supports(req types.SwapQuoteRequest) bool
	// Quote returns a route with Source == Name(). Amounts are decimal
	// strings in each asset's own decimals; the fixed side (FromAmount for
	// send, ToAmount for receive) equals req.Amount. Paths run from
	// req.FromAsset to req.ToAsset with connected hops and shares summing to
	// 1; Fees is [] when there are none. It returns once ctx is done: the
	// router gives each source its own timeout.
	//
	// Errors: ErrNoRoute, ErrInsufficientBalance, ErrRateLimited or
	// ErrBadRequest. Reason maps each error to the reported reason.
	Quote(ctx context.Context, req types.SwapQuoteRequest) (*types.SwapRoute, error)
	// BuildTx returns an unsigned base64 TransactionEnvelope for req.Account
	// that expires timeoutSeconds(req) from now. The user must never receive
	// less than req.MinOut: enforce it in the transaction (classic DestMin) or
	// re-quote and return ErrQuoteMoved. A transaction built by a third party
	// must be checked to do exactly what was quoted (lifi_checks.go), or
	// ErrCheckFailed. The router works out AddsTrustline from the XDR.
	BuildTx(ctx context.Context, req types.SwapBuildRequest) (string, error)
}

var (
	ErrNoRoute             = errors.New("no route")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrRateLimited         = errors.New("rate limited")
	ErrQuoteMoved          = errors.New("quote moved")
	ErrCheckFailed         = errors.New("transaction check failed")
	ErrBadRequest          = errors.New("bad request")
	ErrAccountNotFound     = errors.New("account not found")
)

// Reason maps a Source error to the reason string reported in SwapUnavailable
// and the result label of freighter_swap_source_requests_total.
func Reason(err error) string {
	switch {
	case err == nil:
		return "ok"
	// First: a build wraps the quote-time ErrNoRoute/ErrInsufficientBalance in it.
	case errors.Is(err, ErrQuoteMoved):
		return "quote_moved"
	case errors.Is(err, ErrNoRoute):
		return "no_route"
	case errors.Is(err, ErrInsufficientBalance):
		return "insufficient_balance"
	case errors.Is(err, ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrCheckFailed):
		return "check_failed"
	case errors.Is(err, ErrBadRequest):
		return "bad_request"
	case errors.Is(err, ErrAccountNotFound):
		return "account_not_found"
	}
	return "error"
}
