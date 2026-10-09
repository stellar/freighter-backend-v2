package users

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/stellar/freighter-backend-v2/internal/db"
)

// startMigratedPostgres spins up a throwaway PostgreSQL container, applies the
// embedded migrations, and returns its DSN. Gated behind
// ENABLE_INTEGRATION_TESTS like the db package's tests so `go test ./...` stays
// hermetic.
func startMigratedPostgres(t *testing.T) string {
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
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	_, err = db.Migrate(ctx, dsn, migrate.Up, 0)
	require.NoError(t, err)
	return dsn
}

// ResolveUser against the real schema: a user with a canonical source and one
// linked source resolves from either source to the same user id and the same
// canonical source id; an unlinked source returns the not-found sentinel.
func TestResolveUser_Postgres(t *testing.T) {
	dsn := startMigratedPostgres(t)
	ctx := context.Background()

	pool, err := db.OpenDBConnectionPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	const (
		canonical = "aa" // the phrase that created the user
		linked    = "bb" // an account key linked to it later
		unknown   = "cc" // a key nobody has linked
	)
	var seededUserID uuid.UUID
	err = pool.QueryRow(ctx,
		`INSERT INTO users (canonical_source_id) VALUES ($1) RETURNING id`, canonical).Scan(&seededUserID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO user_sources (source_id, user_id, kind) VALUES ($1, $2, 'phrase'), ($3, $2, 'secret_key')`,
		canonical, seededUserID, linked)
	require.NoError(t, err)

	store := NewStore(pool)

	for _, source := range []string{canonical, linked} {
		var (
			userID       uuid.UUID
			gotCanonical string
		)
		userID, gotCanonical, err = store.ResolveUser(ctx, source)
		require.NoError(t, err, "source %q", source)
		assert.Equal(t, seededUserID, userID, "source %q", source)
		assert.Equal(t, canonical, gotCanonical, "source %q resolves to the cluster's canonical id, not itself", source)
	}

	_, _, err = store.ResolveUser(ctx, unknown)
	assert.ErrorIs(t, err, ErrSourceNotFound)

	// A transaction is a valid backend too, and sees its own uncommitted rows:
	// this is how the link handler resolves the caller mid-transaction.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck // test cleanup
	_, err = tx.Exec(ctx,
		`INSERT INTO user_sources (source_id, user_id, kind) VALUES ($1, $2, 'secret_key')`, unknown, seededUserID)
	require.NoError(t, err)
	userID, gotCanonical, err := NewStore(tx).ResolveUser(ctx, unknown)
	require.NoError(t, err)
	assert.Equal(t, seededUserID, userID)
	assert.Equal(t, canonical, gotCanonical)
}
