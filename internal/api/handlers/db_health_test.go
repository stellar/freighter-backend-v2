package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestCachedDBHealth(t *testing.T) {
	t.Parallel()

	t.Run("serves cached status within the TTL (one ping for many calls)", func(t *testing.T) {
		t.Parallel()
		pinger := &fakeDBPinger{}
		c := newCachedDBHealth(pinger, time.Minute)

		for i := 0; i < 10; i++ {
			assert.Equal(t, types.StatusHealthy, c.Status())
		}
		assert.Equal(t, int32(1), atomic.LoadInt32(&pinger.calls), "should ping once within TTL")
	})

	t.Run("refreshes after the TTL expires", func(t *testing.T) {
		t.Parallel()
		pinger := &fakeDBPinger{}
		c := newCachedDBHealth(pinger, time.Minute)

		now := time.Unix(1_000_000, 0)
		c.now = func() time.Time { return now }

		c.Status()
		now = now.Add(2 * time.Minute) // advance past TTL
		c.Status()

		assert.Equal(t, int32(2), atomic.LoadInt32(&pinger.calls), "should re-ping after TTL")
	})

	t.Run("collapses concurrent refreshes into a single ping", func(t *testing.T) {
		t.Parallel()
		pinger := &blockingPinger{started: make(chan struct{}), release: make(chan struct{})}
		c := newCachedDBHealth(pinger, time.Minute)

		const followers = 20
		var wg sync.WaitGroup

		// Leader enters the ping and blocks, so the cache is cold while followers arrive.
		wg.Add(1)
		go func() { defer wg.Done(); c.Status() }()
		<-pinger.started

		for i := 0; i < followers; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); c.Status() }()
		}

		// Give followers a moment to join the in-flight singleflight call.
		time.Sleep(50 * time.Millisecond)
		close(pinger.release)
		wg.Wait()

		assert.Equal(t, int32(1), atomic.LoadInt32(&pinger.calls), "concurrent callers should share one ping")
	})
}

// blockingPinger blocks in Ping until released, to exercise concurrent refresh.
type blockingPinger struct {
	calls   int32
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (p *blockingPinger) Ping(_ context.Context) error {
	atomic.AddInt32(&p.calls, 1)
	p.once.Do(func() { close(p.started) })
	<-p.release
	return nil
}
