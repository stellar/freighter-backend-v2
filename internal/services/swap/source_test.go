package swap

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		want string
		err  error
	}{
		{"ok", nil},
		{"no_route", fmt.Errorf("x: %w", ErrNoRoute)},
		{"insufficient_balance", ErrInsufficientBalance},
		{"rate_limited", ErrRateLimited},
		{"timeout", context.DeadlineExceeded},
		{"check_failed", fmt.Errorf("fee: %w", ErrCheckFailed)},
		{"quote_moved", ErrQuoteMoved},
		{"quote_moved", fmt.Errorf("lifi: %w: %w", ErrNoRoute, ErrQuoteMoved)},
		{"quote_moved", fmt.Errorf("lifi: %w: %w", ErrInsufficientBalance, ErrQuoteMoved)},
		{"error", errors.New("boom")},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, Reason(tc.err), tc.err)
	}
}
