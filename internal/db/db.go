// Package db wires freighter-backend-v2 to its PostgreSQL database: it opens a
// connection pool, exposes a database/sql bridge for migration tooling, and owns
// the embedded schema migrations. Feature-specific stores build on top of the
// *pgxpool.Pool returned here.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// Pool defaults sized for the ~6k MAU light workload this service serves.
// They mirror the conventions used by the sibling wallet-backend service.
const (
	DefaultMaxConns        int32         = 10
	DefaultMinConns        int32         = 2
	DefaultMaxConnLifetime time.Duration = 5 * time.Minute
	DefaultMaxConnIdleTime time.Duration = 10 * time.Second
)

// PoolConfig holds configurable pgxpool settings. Zero values fall back to the
// Default* constants above.
type PoolConfig struct {
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	// QueryExecMode overrides pgx's default query execution mode. The zero value
	// leaves pgx's default (CacheStatement). Set QueryExecModeExec when connecting
	// through a PgBouncer / CNPG Pooler in transaction pooling mode: the default
	// mode caches server-side prepared statements, which collide across pooled
	// backends and surface as SQLSTATE 42P05 ("prepared statement already exists").
	QueryExecMode pgx.QueryExecMode
}

// DefaultPoolConfig returns a PoolConfig populated with the default values.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxConns:        DefaultMaxConns,
		MinConns:        DefaultMinConns,
		MaxConnLifetime: DefaultMaxConnLifetime,
		MaxConnIdleTime: DefaultMaxConnIdleTime,
	}
}

// resolvePoolConfig returns the first config provided, or DefaultPoolConfig() if
// none. Callers (serve) populate all fields from flags with non-zero defaults;
// this mirrors wallet-backend's resolvePoolConfig for cross-repo parity.
func resolvePoolConfig(configs []PoolConfig) PoolConfig {
	if len(configs) > 0 {
		return configs[0]
	}
	return DefaultPoolConfig()
}

// buildPoolConfig parses the data source name and overlays the resolved pool
// settings onto it. Split out from OpenDBConnectionPool so the mapping (incl.
// the PgBouncer-safe QueryExecMode) is unit-testable without a live database.
func buildPoolConfig(dataSourceName string, poolConfigs ...PoolConfig) (*pgxpool.Config, error) {
	poolCfg := resolvePoolConfig(poolConfigs)

	cfg, err := pgxpool.ParseConfig(dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("parsing DB connection string: %w", err)
	}
	cfg.MaxConns = poolCfg.MaxConns
	cfg.MinConns = poolCfg.MinConns
	cfg.MaxConnLifetime = poolCfg.MaxConnLifetime
	cfg.MaxConnIdleTime = poolCfg.MaxConnIdleTime
	if poolCfg.QueryExecMode != 0 {
		cfg.ConnConfig.DefaultQueryExecMode = poolCfg.QueryExecMode
	}
	return cfg, nil
}

// OpenDBConnectionPool parses the data source name, opens a pgx connection pool,
// and pings it so a bad connection string or unreachable database fails fast at
// startup rather than on the first request.
func OpenDBConnectionPool(ctx context.Context, dataSourceName string, poolConfigs ...PoolConfig) (*pgxpool.Pool, error) {
	cfg, err := buildPoolConfig(dataSourceName, poolConfigs...)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating DB connection pool: %w", err)
	}

	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging DB connection pool: %w", err)
	}

	return pool, nil
}

// SQLDBFromPool returns a *sql.DB backed by the given pgx pool. This is only
// needed for libraries that require database/sql (e.g. sql-migrate).
func SQLDBFromPool(pool *pgxpool.Pool) *sql.DB {
	return stdlib.OpenDBFromPool(pool)
}
