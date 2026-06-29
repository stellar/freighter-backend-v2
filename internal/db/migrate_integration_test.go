package db

import (
	"context"
	"os"
	"testing"

	migrate "github.com/rubenv/sql-migrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// startPostgres spins up a throwaway PostgreSQL container and returns its DSN.
// These tests require Docker and are gated behind ENABLE_INTEGRATION_TESTS so
// the default `go test ./...` run stays hermetic.
func startPostgres(t *testing.T) string {
	t.Helper()
	if os.Getenv("ENABLE_INTEGRATION_TESTS") != "true" {
		t.Skip("set ENABLE_INTEGRATION_TESTS=true to run DB integration tests (requires Docker)")
	}

	ctx := context.Background()
	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("freighter"),
		postgres.WithUsername("freighter"),
		postgres.WithPassword("freighter"),
		// BasicWaitStrategies waits for the "ready to accept connections" log
		// twice — the official image starts, runs init, then restarts — and then
		// the port. A port-only wait can let the first Ping race that restart and
		// flake. See testcontainers' postgres module docs.
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	return dsn
}

func TestMigrate_AppliesAndIsIdempotent(t *testing.T) {
	dsn := startPostgres(t)
	ctx := context.Background()

	// First run applies the embedded migration(s).
	applied, err := Migrate(ctx, dsn, migrate.Up, 0)
	require.NoError(t, err)
	assert.Positive(t, applied, "expected at least one migration to be applied on first run")

	// Re-running must be a no-op: nothing left to apply.
	applied, err = Migrate(ctx, dsn, migrate.Up, 0)
	require.NoError(t, err)
	assert.Zero(t, applied, "re-running migrations should apply nothing (idempotent)")
}

func TestOpenDBConnectionPool_PingsRealDatabase(t *testing.T) {
	dsn := startPostgres(t)
	ctx := context.Background()

	pool, err := OpenDBConnectionPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	require.NoError(t, pool.Ping(ctx))
}
