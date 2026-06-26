package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// fakeDBPinger is a test double for the DB connectivity check. It records
// whether the context it was handed carried a deadline and counts calls.
type fakeDBPinger struct {
	err         error
	hadDeadline bool
	calls       int32
}

func (f *fakeDBPinger) Ping(ctx context.Context) error {
	atomic.AddInt32(&f.calls, 1)
	_, f.hadDeadline = ctx.Deadline()
	return f.err
}

func TestDBHealthHandler_CheckDBHealth(t *testing.T) {
	t.Parallel()

	t.Run("returns 200 and healthy when DB is reachable", func(t *testing.T) {
		t.Parallel()
		handler := NewDBHealthHandler(&fakeDBPinger{err: nil})

		req, _ := http.NewRequest("GET", "/api/v1/db-health", nil)
		rr := httptest.NewRecorder()

		err := handler.CheckDBHealth(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp types.GetHealthResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		assert.Equal(t, types.StatusHealthy, resp.Status)
	})

	t.Run("returns 200 and unhealthy when DB is unreachable", func(t *testing.T) {
		t.Parallel()
		handler := NewDBHealthHandler(&fakeDBPinger{err: errors.New("connection refused")})

		req, _ := http.NewRequest("GET", "/api/v1/db-health", nil)
		rr := httptest.NewRecorder()

		err := handler.CheckDBHealth(rr, req)
		require.NoError(t, err)
		// Mirrors rpc-health: the request itself never fails; status is in the body
		// so consuming probes don't restart or depool the pod over a DB outage.
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp types.GetHealthResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		assert.Equal(t, types.StatusUnhealthy, resp.Status)
	})

	t.Run("pings once per request", func(t *testing.T) {
		t.Parallel()
		pinger := &fakeDBPinger{err: nil}
		handler := NewDBHealthHandler(pinger)

		for i := 0; i < 3; i++ {
			req, _ := http.NewRequest("GET", "/api/v1/db-health", nil)
			require.NoError(t, handler.CheckDBHealth(httptest.NewRecorder(), req))
		}
		assert.Equal(t, int32(3), atomic.LoadInt32(&pinger.calls), "each request should ping the DB")
	})

	t.Run("bounds the ping with a deadline so it can't hold a connection indefinitely", func(t *testing.T) {
		t.Parallel()
		pinger := &fakeDBPinger{err: nil}
		handler := NewDBHealthHandler(pinger)

		req, _ := http.NewRequest("GET", "/api/v1/db-health", nil)
		err := handler.CheckDBHealth(httptest.NewRecorder(), req)
		require.NoError(t, err)
		assert.True(t, pinger.hadDeadline, "Ping should receive a context with a deadline")
	})
}
