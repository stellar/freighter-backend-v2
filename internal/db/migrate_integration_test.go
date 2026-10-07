package db

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// userSchemaObjects lists the relations the users/user_sources migration must
// create and its down migration must remove.
var userSchemaTables = []string{"users", "user_sources"}

const userSchemaIndex = "user_sources_user_id_idx"

// The UNIQUE constraint on users.canonical_source_id doubles as its lookup index.
const usersCanonicalSourceIndex = "users_canonical_source_id_key"

func TestMigrate_UsersSchema_UpCreatesTablesAndIndex(t *testing.T) {
	dsn := startPostgres(t)
	ctx := context.Background()

	_, err := Migrate(ctx, dsn, migrate.Up, 0)
	require.NoError(t, err)

	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer conn.Close(ctx) //nolint:errcheck // test cleanup

	for _, table := range userSchemaTables {
		assert.True(t, tableExists(t, ctx, conn, table), "table %q should exist after up", table)
	}
	assert.True(t, indexExists(t, ctx, conn, userSchemaIndex), "index %q should exist after up", userSchemaIndex)
	assert.True(t, indexExists(t, ctx, conn, usersCanonicalSourceIndex), "unique index %q should exist after up", usersCanonicalSourceIndex)

	// users.id defaults to a generated UUID and canonical_source_id is required.
	var userID string
	err = conn.QueryRow(ctx,
		`INSERT INTO users (canonical_source_id) VALUES ('aa') RETURNING id::text`).Scan(&userID)
	require.NoError(t, err)
	assert.Len(t, userID, 36, "users.id should default to a UUID")

	// canonical_source_id is the exposed user id, so two users can't share one.
	_, err = conn.Exec(ctx, `INSERT INTO users (canonical_source_id) VALUES ('aa')`)
	require.Error(t, err, "duplicate canonical_source_id should be rejected")
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23505", pgErr.Code, "expected unique_violation")

	// user_sources rows hang off users(id) and are removed with their user.
	_, err = conn.Exec(ctx,
		`INSERT INTO user_sources (source_id, user_id, kind) VALUES ('aa', $1, 'phrase')`, userID)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	require.NoError(t, err)
	var remaining int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM user_sources`).Scan(&remaining))
	assert.Zero(t, remaining, "deleting a user should cascade to its user_sources")
}

func TestMigrate_UsersSchema_DownThenUpSucceeds(t *testing.T) {
	dsn := startPostgres(t)
	ctx := context.Background()

	_, err := Migrate(ctx, dsn, migrate.Up, 0)
	require.NoError(t, err)

	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer conn.Close(ctx) //nolint:errcheck // test cleanup

	// Roll back just the latest migration (the users schema), not the initial one.
	applied, err := Migrate(ctx, dsn, migrate.Down, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, applied)
	for _, table := range userSchemaTables {
		assert.False(t, tableExists(t, ctx, conn, table), "table %q should be gone after down", table)
	}
	assert.False(t, indexExists(t, ctx, conn, userSchemaIndex), "index %q should be gone after down", userSchemaIndex)

	// Re-applying restores the schema (v2 Verification item 6: down then up).
	applied, err = Migrate(ctx, dsn, migrate.Up, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, applied)
	for _, table := range userSchemaTables {
		assert.True(t, tableExists(t, ctx, conn, table), "table %q should exist after re-up", table)
	}
}

func tableExists(t *testing.T, ctx context.Context, conn *pgx.Conn, table string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'public' AND tablename = $1)`, table).Scan(&exists)
	require.NoError(t, err)
	return exists
}

func indexExists(t *testing.T, ctx context.Context, conn *pgx.Conn, index string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1)`, index).Scan(&exists)
	require.NoError(t, err)
	return exists
}
