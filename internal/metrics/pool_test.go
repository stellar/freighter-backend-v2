package metrics

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterDBPoolMetrics(t *testing.T) {
	t.Parallel()

	// A pool against an unreachable DSN never connects (MinConns defaults to 0),
	// but pool.Stat() is safe to call, so the gauge/counter funcs work without a
	// real database. This verifies the metrics are registered and gatherable.
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/db")
	require.NoError(t, err)
	defer pool.Close()

	reg := prometheus.NewRegistry()
	RegisterDBPoolMetrics(reg, pool)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	got := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		got[mf.GetName()] = true
	}
	for _, want := range []string{
		"freighter_db_pool_acquired_conns",
		"freighter_db_pool_idle_conns",
		"freighter_db_pool_max_conns",
		"freighter_db_pool_acquire_total",
		"freighter_db_pool_canceled_acquire_total",
	} {
		assert.True(t, got[want], "expected metric %q to be registered", want)
	}
}
