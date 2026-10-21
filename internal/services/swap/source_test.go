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
	cases := map[string]error{
		"ok":                   nil,
		"no_route":             fmt.Errorf("x: %w", ErrNoRoute),
		"insufficient_balance": ErrInsufficientBalance,
		"rate_limited":         ErrRateLimited,
		"timeout":              context.DeadlineExceeded,
		"check_failed":         fmt.Errorf("fee: %w", ErrCheckFailed),
		"error":                errors.New("boom"),
	}
	for want, err := range cases {
		assert.Equal(t, want, Reason(err), want)
	}
}
