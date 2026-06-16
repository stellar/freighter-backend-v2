package handlers

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	// dbHealthPingTimeout bounds each connectivity probe so a slow database can't
	// make the health check hold a pool connection for long.
	dbHealthPingTimeout = 2 * time.Second
	// dbHealthCacheTTL caches the result so a high volume of (external, client-side)
	// health requests doesn't translate into a DB ping per request — decoupling
	// request rate from DB load and removing the pool-exhaustion DoS vector. A few
	// seconds of staleness is fine for a feature-availability check.
	dbHealthCacheTTL = 5 * time.Second
)

// DBPinger reports whether the backing database is reachable.
// *pgxpool.Pool satisfies this interface directly.
type DBPinger interface {
	Ping(ctx context.Context) error
}

// cachedDBHealth caches DB connectivity status for dbHealthCacheTTL. Concurrent
// refreshes are collapsed into a single ping via singleflight, so a burst of
// requests at cache expiry produces one DB round-trip, not one per request.
type cachedDBHealth struct {
	db    DBPinger
	ttl   time.Duration
	now   func() time.Time
	group singleflight.Group

	mu        sync.Mutex
	status    string
	checkedAt time.Time
	hasValue  bool
}

func newCachedDBHealth(db DBPinger, ttl time.Duration) *cachedDBHealth {
	return &cachedDBHealth{db: db, ttl: ttl, now: time.Now}
}

func (c *cachedDBHealth) fresh() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasValue && c.now().Sub(c.checkedAt) < c.ttl {
		return c.status, true
	}
	return "", false
}

// Status returns the cached status if fresh, otherwise refreshes it. The refresh
// runs on a background context bounded by dbHealthPingTimeout (so it isn't tied
// to any single caller's request lifetime) and is de-duplicated across concurrent
// callers.
func (c *cachedDBHealth) Status() string {
	if s, ok := c.fresh(); ok {
		return s
	}

	v, _, _ := c.group.Do("db-health", func() (interface{}, error) {
		// Another caller may have refreshed while we waited to lead the group.
		if s, ok := c.fresh(); ok {
			return s, nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), dbHealthPingTimeout)
		defer cancel()

		status := types.StatusHealthy
		if err := c.db.Ping(ctx); err != nil {
			status = types.StatusUnhealthy
		}

		c.mu.Lock()
		c.status = status
		c.checkedAt = c.now()
		c.hasValue = true
		c.mu.Unlock()
		return status, nil
	})
	return v.(string)
}

type DBHealthHandler struct {
	health *cachedDBHealth
}

func NewDBHealthHandler(db DBPinger) *DBHealthHandler {
	return &DBHealthHandler{health: newCachedDBHealth(db, dbHealthCacheTTL)}
}

// CheckDBHealth reports database connectivity. Like the rpc-health endpoint, it
// always returns HTTP 200 and conveys reachability via the body's status field,
// so clients can probe whether DB-backed features are available without a DB
// outage ever restarting or depooling an otherwise-serving pod. The status is
// cached (see cachedDBHealth) so external request volume can't exhaust the pool.
func (h *DBHealthHandler) CheckDBHealth(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	resp := types.GetHealthResponse{Status: h.health.Status()}
	if err := response.JSON(w, http.StatusOK, resp); err != nil {
		return httperror.InternalServerError("error writing DB health check response", err)
	}
	return nil
}
