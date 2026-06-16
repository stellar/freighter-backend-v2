package db

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	migrate "github.com/rubenv/sql-migrate"

	"github.com/stellar/freighter-backend-v2/internal/db/migrations"
)

// Migrate applies the embedded migrations against the database at databaseURL in
// the given direction, up to count migrations (0 means "all"). It returns the
// number of migrations actually applied, which is 0 on a re-run once the schema
// is up to date — i.e. the operation is idempotent.
func Migrate(ctx context.Context, databaseURL string, direction migrate.MigrationDirection, count int) (int, error) {
	// Use the same PgBouncer-safe exec mode serve uses: a deploy migrate Job may
	// share serve's DATABASE_URL (pointing at the CNPG Pooler in transaction
	// mode), where the default statement-cache mode throws 42P05. Default sizing
	// is fine for a one-shot migration.
	poolCfg := DefaultPoolConfig()
	poolCfg.QueryExecMode = pgx.QueryExecModeExec
	pool, err := OpenDBConnectionPool(ctx, databaseURL, poolCfg)
	if err != nil {
		return 0, fmt.Errorf("connecting to the database: %w", err)
	}
	defer pool.Close()

	sqlDB := SQLDBFromPool(pool)
	defer sqlDB.Close() //nolint:errcheck // best-effort close of the sql.DB wrapper

	source := migrate.HttpFileSystemMigrationSource{FileSystem: http.FS(migrations.FS)}

	applied, err := migrate.ExecMaxContext(ctx, sqlDB, "postgres", source, direction, count)
	if err != nil {
		return applied, fmt.Errorf("applying migrations: %w", err)
	}
	return applied, nil
}
