package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenDBConnectionPool_InvalidDSN(t *testing.T) {
	t.Parallel()

	// A DSN with a non-numeric port can't be parsed, so the pool must fail fast
	// rather than returning a half-initialized pool.
	_, err := OpenDBConnectionPool(context.Background(), "postgres://user:pass@localhost:notaport/db")
	require.Error(t, err)
}
