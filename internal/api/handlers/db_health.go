package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// dbHealthPingTimeout bounds each connectivity probe so a slow database can't
// make the health check hold a pool connection for long.
const dbHealthPingTimeout = 2 * time.Second

// DBPinger reports whether the backing database is reachable.
// *pgxpool.Pool satisfies this interface directly.
type DBPinger interface {
	Ping(ctx context.Context) error
}

type DBHealthHandler struct {
	db DBPinger
}

func NewDBHealthHandler(db DBPinger) *DBHealthHandler {
	return &DBHealthHandler{db: db}
}

// CheckDBHealth reports database connectivity. Like the rpc-health endpoint, it
// does a live ping per request and always returns HTTP 200, conveying
// reachability via the body's status field — so clients can probe whether
// DB-backed features are available without a DB outage ever restarting or
// depooling an otherwise-serving pod. The ping is bounded by dbHealthPingTimeout
// so a slow database can't tie up a pool connection.
func (h *DBHealthHandler) CheckDBHealth(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	// A nil pinger means the database is disabled (DB_ENABLED=false / no pool
	// opened). Report that distinctly rather than pinging a nil pool.
	if h.db == nil {
		resp := types.GetHealthResponse{Status: types.StatusDisabled}
		if err := response.JSON(w, http.StatusOK, resp); err != nil {
			return httperror.InternalServerError("error writing DB health check response", err)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(r.Context(), dbHealthPingTimeout)
	defer cancel()

	status := types.StatusHealthy
	if err := h.db.Ping(ctx); err != nil {
		status = types.StatusUnhealthy
	}

	resp := types.GetHealthResponse{Status: status}
	if err := response.JSON(w, http.StatusOK, resp); err != nil {
		return httperror.InternalServerError("error writing DB health check response", err)
	}
	return nil
}
