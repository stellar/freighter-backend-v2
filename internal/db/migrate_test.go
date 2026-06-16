package db

import (
	"context"
	"testing"

	migrate "github.com/rubenv/sql-migrate"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/db/migrations"
)

func TestMigrate_InvalidDSN(t *testing.T) {
	t.Parallel()

	// Migration on boot must fail fast on an unparseable connection string.
	_, err := Migrate(context.Background(), "postgres://u:p@localhost:notaport/db", migrate.Up, 0)
	require.Error(t, err)
}

func TestMigrations_EmbeddedSourceIsNonEmpty(t *testing.T) {
	t.Parallel()

	// The migration tooling needs at least the initial migration embedded so the
	// step actually runs on boot. Feature schemas are added in their own issues.
	entries, err := migrations.FS.ReadDir(".")
	require.NoError(t, err)
	require.NotEmpty(t, entries, "expected at least one embedded .sql migration")
}
