package users

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// classifyTimeout must turn the two SQLSTATEs the transaction's SET LOCAL
// timeouts raise into their sentinels, keep the original error in the chain,
// and leave every other error alone.
func TestClassifyTimeout(t *testing.T) {
	lock := &pgconn.PgError{Code: "55P03", Message: "canceling statement due to lock timeout"}
	stmt := &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}
	unique := &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
	plain := errors.New("connection reset")

	for _, tc := range []struct {
		name string
		in   error
		want error // sentinel expected in the chain; nil means unchanged
	}{
		{"lock timeout", fmt.Errorf("locking signer x: %w", lock), ErrLockTimeout},
		{"statement timeout", fmt.Errorf("inserting sources: %w", stmt), ErrStatementTimeout},
		{"other pg error", fmt.Errorf("creating user: %w", unique), nil},
		{"non-pg error", plain, nil},
		{"context deadline", fmt.Errorf("x: %w", context.DeadlineExceeded), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyTimeout(tc.in)
			require.Error(t, got)
			if tc.want == nil {
				assert.Same(t, tc.in, got, "returned unchanged")
				assert.NotErrorIs(t, got, ErrLockTimeout)
				assert.NotErrorIs(t, got, ErrStatementTimeout)
				return
			}
			assert.ErrorIs(t, got, tc.want)
			var pgErr *pgconn.PgError
			assert.ErrorAs(t, got, &pgErr, "the Postgres error stays in the chain for the log line")
			assert.ErrorIs(t, got, tc.in)
		})
	}
	assert.Nil(t, classifyTimeout(nil))
}
