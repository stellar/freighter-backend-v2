package metrics

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// RegisterDBPoolMetrics registers Prometheus metrics for pgxpool connection pool
// statistics on the given registry. Without these, pool saturation/exhaustion —
// the failure mode the db-health endpoint reports — is invisible to monitoring.
// Mirrors the sibling wallet-backend metrics (with the freighter_ prefix).
//
// Gauges — point-in-time connection state:
//   - freighter_db_pool_acquired_conns — connections currently checked out.
//   - freighter_db_pool_idle_conns — connections available for immediate use.
//   - freighter_db_pool_constructing_conns — connections being established.
//   - freighter_db_pool_total_conns — all open connections.
//   - freighter_db_pool_max_conns — configured pool size limit (utilization = acquired/max).
//
// Counters — monotonic acquisition/lifecycle stats:
//   - freighter_db_pool_acquire_total — total acquisitions.
//   - freighter_db_pool_acquire_wait_seconds_total — cumulative acquire wait time.
//   - freighter_db_pool_empty_acquire_total — acquires that found no idle conn (undersized pool signal).
//   - freighter_db_pool_canceled_acquire_total — acquires canceled by context (alert-worthy: gave up waiting).
//   - freighter_db_pool_new_conns_total — new connections created (churn signal).
func RegisterDBPoolMetrics(reg prometheus.Registerer, pool *pgxpool.Pool) {
	reg.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "freighter_db_pool_acquired_conns",
			Help: "Number of currently acquired connections.",
		}, func() float64 { return float64(pool.Stat().AcquiredConns()) }),

		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "freighter_db_pool_idle_conns",
			Help: "Number of currently idle connections.",
		}, func() float64 { return float64(pool.Stat().IdleConns()) }),

		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "freighter_db_pool_constructing_conns",
			Help: "Number of connections currently being established.",
		}, func() float64 { return float64(pool.Stat().ConstructingConns()) }),

		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "freighter_db_pool_total_conns",
			Help: "Total number of connections currently open.",
		}, func() float64 { return float64(pool.Stat().TotalConns()) }),

		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "freighter_db_pool_max_conns",
			Help: "Maximum number of connections allowed.",
		}, func() float64 { return float64(pool.Stat().MaxConns()) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "freighter_db_pool_acquire_total",
			Help: "Total number of connection acquisitions from the pool.",
		}, func() float64 { return float64(pool.Stat().AcquireCount()) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "freighter_db_pool_acquire_wait_seconds_total",
			Help: "Total cumulative time spent waiting to acquire connections.",
		}, func() float64 { return pool.Stat().AcquireDuration().Seconds() }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "freighter_db_pool_empty_acquire_total",
			Help: "Total acquires that waited because no idle connection was available.",
		}, func() float64 { return float64(pool.Stat().EmptyAcquireCount()) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "freighter_db_pool_canceled_acquire_total",
			Help: "Total connection acquisitions canceled by context.",
		}, func() float64 { return float64(pool.Stat().CanceledAcquireCount()) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "freighter_db_pool_new_conns_total",
			Help: "Total number of new connections created.",
		}, func() float64 { return float64(pool.Stat().NewConnsCount()) }),
	)
}
