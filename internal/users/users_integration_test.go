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

// ListSources against the real schema: exactly the rows under the user, in
// (created_at, source_id) order with their kinds; nothing for an unknown user;
// and a transaction backend sees its own uncommitted rows, which is how Link
// returns the cluster it just wrote.
func TestListSources_Postgres(t *testing.T) {
	dsn := startMigratedPostgres(t)
	ctx := context.Background()

	pool, err := db.OpenDBConnectionPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	var u1, u2 uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (canonical_source_id) VALUES ('aa') RETURNING id`).Scan(&u1))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (canonical_source_id) VALUES ('cc') RETURNING id`).Scan(&u2))
	// bb is inserted first with a later created_at than aa would get by default,
	// so the order asserted below is created_at's, not insertion's.
	_, err = pool.Exec(ctx,
		`INSERT INTO user_sources (source_id, user_id, kind, created_at) VALUES
		   ('bb', $1, 'secret_key', now() + interval '1 minute'),
		   ('aa', $1, 'phrase',     now()),
		   ('cc', $2, 'phrase',     now())`, u1, u2)
	require.NoError(t, err)

	store := NewStore(pool)

	got, err := store.ListSources(ctx, u1)
	require.NoError(t, err)
	require.Len(t, got, 2, "only u1's rows: %+v", got)
	assert.Equal(t, "aa", got[0].ID)
	assert.Equal(t, "phrase", got[0].Kind)
	assert.Equal(t, "bb", got[1].ID)
	assert.Equal(t, "secret_key", got[1].Kind)
	assert.False(t, got[0].CreatedAt.IsZero())
	assert.True(t, got[0].CreatedAt.Before(got[1].CreatedAt), "ordered by created_at")

	empty, err := store.ListSources(ctx, uuid.New())
	require.NoError(t, err)
	require.NotNil(t, empty)
	assert.Empty(t, empty)

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck // test cleanup
	_, err = tx.Exec(ctx,
		`INSERT INTO user_sources (source_id, user_id, kind) VALUES ('dd', $1, 'secret_key')`, u1)
	require.NoError(t, err)
	inTx, err := NewStore(tx).ListSources(ctx, u1)
	require.NoError(t, err)
	assert.Len(t, inTx, 3, "the transaction sees its own uncommitted row")
	outside, err := store.ListSources(ctx, u1)
	require.NoError(t, err)
	assert.Len(t, outside, 2, "the pool does not")
}
