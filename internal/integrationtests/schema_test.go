package integrationtests

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/stellar/freighter-backend-v2/internal/integrationtests/infrastructure"
)

// SchemaTestSuite checks that the app database the backend is pointed at has
// been migrated by the harness, so the other suites run against the real schema
// (serve only pings the DB on boot and would otherwise start "healthy" against
// an empty one).
type SchemaTestSuite struct {
	suite.Suite
	appPostgresContainer *infrastructure.TestContainer
}

func (s *SchemaTestSuite) TestAppDatabaseIsMigrated() {
	t := s.T()
	ctx := context.Background()

	dsn, err := infrastructure.AppDatabaseHostURL(ctx, s.appPostgresContainer)
	require.NoError(t, err)

	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer conn.Close(ctx) //nolint:errcheck // test cleanup

	for _, table := range []string{"users", "user_sources"} {
		var exists bool
		err = conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'public' AND tablename = $1)`, table).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "table %q should exist: the harness must run migrations before serve starts", table)
	}
}
