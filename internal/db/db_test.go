package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildPoolConfig_AppliesQueryExecMode(t *testing.T) {
	t.Parallel()

	dsn := "postgres://u:p@localhost:5432/db"

	t.Run("explicit QueryExecModeExec is applied when set", func(t *testing.T) {
		t.Parallel()
		cfg, err := buildPoolConfig(dsn, PoolConfig{QueryExecMode: pgx.QueryExecModeExec})
		require.NoError(t, err)
		assert.Equal(t, pgx.QueryExecModeExec, cfg.ConnConfig.DefaultQueryExecMode)
	})

	t.Run("zero QueryExecMode leaves pgx's default untouched", func(t *testing.T) {
		t.Parallel()
		cfg, err := buildPoolConfig(dsn, PoolConfig{})
		require.NoError(t, err)
		// pgx's default exec mode is CacheStatement (the zero value).
		assert.Equal(t, pgx.QueryExecModeCacheStatement, cfg.ConnConfig.DefaultQueryExecMode)
	})

	t.Run("pool sizing fields flow through", func(t *testing.T) {
		t.Parallel()
		cfg, err := buildPoolConfig(dsn, PoolConfig{MaxConns: 7, MinConns: 3})
		require.NoError(t, err)
		assert.Equal(t, int32(7), cfg.MaxConns)
		assert.Equal(t, int32(3), cfg.MinConns)
	})
}

func TestOpenDBConnectionPool_InvalidDSN(t *testing.T) {
	t.Parallel()

	// A DSN with a non-numeric port can't be parsed, so the pool must fail fast
	// rather than returning a half-initialized pool.
	_, err := OpenDBConnectionPool(context.Background(), "postgres://user:pass@localhost:notaport/db")
	require.Error(t, err)
}
