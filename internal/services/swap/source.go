package swap

import (
	"context"
	"errors"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// Source is one liquidity venue that can quote a swap and build its transaction.
type Source interface {
	Name() string
	Supports(network string) bool
	Quote(ctx context.Context, req types.SwapQuoteRequest) (*types.SwapRoute, error)
	BuildTx(ctx context.Context, req types.SwapBuildRequest) (string, error)
}

var (
	ErrNoRoute             = errors.New("no route")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrRateLimited         = errors.New("rate limited")
	ErrUnsupportedMode     = errors.New("unsupported mode")
	ErrQuoteMoved          = errors.New("quote moved")
	ErrCheckFailed         = errors.New("transaction check failed")
	ErrBadRequest          = errors.New("bad request")
	ErrAccountNotFound     = errors.New("account not found")
)

// Reason maps a Source error to the reason string reported in SwapUnavailable.
func Reason(err error) string {
	switch {
	case err == nil:
		return "ok"
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
	}
	return "error"
}
