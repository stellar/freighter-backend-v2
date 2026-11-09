package swap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

type fakeSource struct {
	name string
	// supports is Supports; nil serves every request.
	supports func(types.SwapQuoteRequest) bool
	route    *types.SwapRoute
	err      error
	delay    time.Duration
	buildXDR string
	buildErr error
	// buildBlock makes BuildTx wait for ctx to end, then call afterBlock.
	buildBlock bool
	afterBlock func()
	panics     bool
	calls      int
	builds     int
	// lateOK makes Quote wait for ctx to end, then succeed anyway.
	lateOK bool
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Supports(req types.SwapQuoteRequest) bool {
	return f.supports == nil || f.supports(req)
}

func (f *fakeSource) BuildTx(ctx context.Context, _ types.SwapBuildRequest) (string, error) {
	f.builds++
	if f.panics {
		panic("build boom")
	}
	if f.buildBlock {
		<-ctx.Done()
		if f.afterBlock != nil {
			f.afterBlock()
		}
		return "", ctx.Err()
	}
	return f.buildXDR, f.buildErr
}

func (f *fakeSource) Quote(ctx context.Context, _ types.SwapQuoteRequest) (*types.SwapRoute, error) {
	f.calls++
	if f.panics {
		panic("quote boom")
	}
	if f.lateOK {
		<-ctx.Done()
		return f.route, nil
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.route, f.err
}

func route(src, from, to string) *types.SwapRoute {
	return &types.SwapRoute{
		Source: src, FromAmount: from, ToAmount: to,
		Paths: []types.SwapPath{{Share: "1", Hops: []types.SwapHop{{Venue: "stellar-dex", From: "XLM", To: "USDC:" + usdcIssuer}}}},
	}
}

var sendReq = types.SwapQuoteRequest{
	Network: types.PUBLIC, Account: "GA…", Mode: types.SwapModeSend,
	FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100",
}

func TestRouterRanksSendByMostOut(t *testing.T) {
	t.Parallel()
	m := metrics.NewSwap(prometheus.NewRegistry())
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", route: route("lifi", "100", "17.80")},
		&fakeSource{name: "classic", route: route("classic", "100", "17.90")},
	}, time.Second, time.Second, m)
	res, err := r.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	require.Len(t, res.Routes, 2)
	assert.Equal(t, "classic", res.Routes[0].Source)
	assert.Empty(t, res.Unavailable)
	assert.Equal(t, 1.0, testutil.ToFloat64(m.Winners.WithLabelValues("classic", "classic")))
}

func TestRouterRanksReceiveByLeastIn(t *testing.T) {
	t.Parallel()
	r := NewRouter([]Source{
		&fakeSource{name: "a", route: route("a", "3.0", "0.5")},
		&fakeSource{name: "b", route: route("b", "2.9", "0.5")},
	}, time.Second, time.Second, nil)
	req := sendReq
	req.Mode = types.SwapModeReceive
	res, err := r.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "b", res.Routes[0].Source)
}

func TestRouterOneSourceFailingDoesNotFailRequest(t *testing.T) {
	t.Parallel()
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", err: ErrRateLimited},
		&fakeSource{name: "classic", route: route("classic", "100", "17.9")},
	}, time.Second, time.Second, nil)
	res, err := r.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	require.Len(t, res.Routes, 1)
	assert.Equal(t, []types.SwapUnavailable{{Source: "lifi", Reason: "rate_limited"}}, res.Unavailable)
}

func TestRouterAllNoRouteIsEmpty200(t *testing.T) {
	t.Parallel()
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", err: ErrNoRoute},
		&fakeSource{name: "classic", err: ErrNoRoute},
	}, time.Second, time.Second, nil)
	res, err := r.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	assert.Empty(t, res.Routes)
	assert.Len(t, res.Unavailable, 2)
}

// A bad request is the caller's fault, not a source failure.
func TestRouterBadRequestIsNotASourceFailure(t *testing.T) {
	t.Parallel()
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", err: fmt.Errorf("amount: %w", ErrBadRequest)},
		&fakeSource{name: "classic", err: errors.New("boom")},
	}, time.Second, time.Second, nil)
	res, err := r.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	assert.Equal(t, []types.SwapUnavailable{{Source: "lifi", Reason: "bad_request"}, {Source: "classic", Reason: "error"}}, res.Unavailable)
}

// Input every source rejects can never quote; that is the caller's error, not "no route".
func TestRouterAllBadRequestIsBadRequest(t *testing.T) {
	t.Parallel()
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", err: fmt.Errorf("amount: %w", ErrBadRequest)},
		&fakeSource{name: "classic", err: fmt.Errorf("asset: %w", ErrBadRequest)},
	}, time.Second, time.Second, nil)
	_, err := r.Quote(context.Background(), sendReq)
	assert.ErrorIs(t, err, ErrBadRequest)

	mixed := NewRouter([]Source{
		&fakeSource{name: "lifi", err: fmt.Errorf("amount: %w", ErrBadRequest)},
		&fakeSource{name: "classic", err: ErrNoRoute},
	}, time.Second, time.Second, nil)
	res, err := mixed.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	assert.Empty(t, res.Routes)
}

func TestRouterAllErroringIsAllSourcesFailed(t *testing.T) {
	t.Parallel()
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", err: errors.New("boom")},
		&fakeSource{name: "classic", err: errors.New("boom")},
	}, time.Second, time.Second, nil)
	_, err := r.Quote(context.Background(), sendReq)
	assert.ErrorIs(t, err, ErrAllSourcesFailed)
}

func TestRouterUnsupportedSourceIsNotAsked(t *testing.T) {
	t.Parallel()
	lifi := &fakeSource{name: "lifi", supports: func(req types.SwapQuoteRequest) bool { return req.Network == types.PUBLIC }}
	r := NewRouter([]Source{lifi, &fakeSource{name: "classic", route: route("classic", "1", "1")}}, time.Second, time.Second, nil)

	req := sendReq
	req.Network = types.TESTNET
	res, err := r.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, []types.SwapUnavailable{{Source: "lifi", Reason: "unsupported"}}, res.Unavailable)
	assert.Equal(t, 0, lifi.calls)
}

// A hung source doesn't hold the response past the timeout.
func TestRouterHungSourceTimesOut(t *testing.T) {
	t.Parallel()
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", delay: time.Hour},
		&fakeSource{name: "classic", route: route("classic", "100", "17.9")},
	}, 50*time.Millisecond, time.Second, nil)
	start := time.Now()
	res, err := r.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second)
	require.Len(t, res.Routes, 1)
	assert.Equal(t, []types.SwapUnavailable{{Source: "lifi", Reason: "timeout"}}, res.Unavailable)
}

func TestRouterNilRouteIsError(t *testing.T) {
	t.Parallel()
	r := NewRouter([]Source{
		&fakeSource{name: "lifi"},
		&fakeSource{name: "classic", route: route("classic", "100", "17.9")},
	}, time.Second, time.Second, nil)
	res, err := r.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	require.Len(t, res.Routes, 1)
	assert.Equal(t, []types.SwapUnavailable{{Source: "lifi", Reason: "error"}}, res.Unavailable)

	r = NewRouter([]Source{&fakeSource{name: "lifi"}}, time.Second, time.Second, nil)
	_, err = r.Quote(context.Background(), sendReq)
	assert.ErrorIs(t, err, ErrAllSourcesFailed)
}

func TestRouterUnparseableRankingAmountIsDropped(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode      string
		bad, good *types.SwapRoute
	}{
		{types.SwapModeSend, route("bad", "100", "abc"), route("good", "100", "17.9")},
		{types.SwapModeReceive, route("bad", "abc", "1"), route("good", "3", "1")},
		{types.SwapModeSend, route("bad", "100", "0"), route("good", "100", "17.9")},
		{types.SwapModeReceive, route("bad", "-1", "1"), route("good", "3", "1")},
	} {
		r := NewRouter([]Source{
			&fakeSource{name: "bad", route: tc.bad},
			&fakeSource{name: "good", route: tc.good},
		}, time.Second, time.Second, nil)
		req := sendReq
		req.Mode = tc.mode
		res, err := r.Quote(context.Background(), req)
		require.NoError(t, err, tc.mode)
		require.Len(t, res.Routes, 1, tc.mode)
		assert.Equal(t, "good", res.Routes[0].Source, tc.mode)
		assert.Equal(t, []types.SwapUnavailable{{Source: "bad", Reason: "error"}}, res.Unavailable, tc.mode)
	}
}

func TestRouterBuildDispatchesAndDetectsTrustline(t *testing.T) {
	t.Parallel()
	x, acct := classicTx(t, true)
	r := NewRouter([]Source{&fakeSource{name: "classic", buildXDR: x}}, time.Second, time.Second, nil)
	res, err := r.Build(context.Background(), types.SwapBuildRequest{
		Network: types.PUBLIC, Account: acct,
		Source: "classic", FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer,
	})
	require.NoError(t, err)
	assert.Equal(t, x, res.XDR)
	assert.True(t, res.AddsTrustline)

	_, err = r.Build(context.Background(), types.SwapBuildRequest{Network: types.PUBLIC, Source: "nope"})
	assert.ErrorIs(t, err, ErrBadRequest)
}

// A client disconnect is not a source failure: no source metric, no
// ErrAllSourcesFailed.
func TestRouterQuoteParentCancelled(t *testing.T) {
	t.Parallel()
	m := metrics.NewSwap(prometheus.NewRegistry())
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", delay: time.Hour},
		&fakeSource{name: "classic", delay: time.Hour},
	}, time.Hour, time.Second, m)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	_, err := r.Quote(ctx, sendReq)
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrAllSourcesFailed)
	assert.Equal(t, 0, testutil.CollectAndCount(m.SourceRequests))
}

func TestRouterBuildTimesOut(t *testing.T) {
	t.Parallel()
	m := metrics.NewSwap(prometheus.NewRegistry())
	r := NewRouter([]Source{&fakeSource{name: "lifi", buildBlock: true}}, time.Second, 50*time.Millisecond, m)
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := r.Build(context.Background(), types.SwapBuildRequest{Network: types.PUBLIC, Source: "lifi"})
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Less(t, time.Since(start), time.Second)
		assert.Equal(t, 1.0, testutil.ToFloat64(m.SourceRequests.WithLabelValues("lifi", "build", "timeout")))
	case <-time.After(2 * time.Second):
		t.Fatal("Build did not return after the build timeout")
	}
}

func TestRouterBuildParentCancelled(t *testing.T) {
	t.Parallel()
	m := metrics.NewSwap(prometheus.NewRegistry())
	r := NewRouter([]Source{&fakeSource{name: "lifi", buildBlock: true}}, time.Second, time.Hour, m)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	_, err := r.Build(ctx, types.SwapBuildRequest{Network: types.PUBLIC, Source: "lifi"})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, testutil.CollectAndCount(m.SourceRequests))
}

// The client leaving as the build budget runs out is still our timeout.
func TestRouterBuildDeadlineWinsOverParentCancel(t *testing.T) {
	t.Parallel()
	m := metrics.NewSwap(prometheus.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &fakeSource{name: "lifi", buildBlock: true, afterBlock: cancel}
	r := NewRouter([]Source{src}, time.Second, 20*time.Millisecond, m)
	_, err := r.Build(ctx, types.SwapBuildRequest{Network: types.PUBLIC, Source: "lifi"})
	require.Error(t, ctx.Err())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1.0, testutil.ToFloat64(m.SourceRequests.WithLabelValues("lifi", "build", "timeout")))
}

// Not parallel: it swaps the global logger's output.
func TestRouterLogsSourceFailures(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })

	r := NewRouter([]Source{
		&fakeSource{name: "lifi", err: ErrRateLimited},
		&fakeSource{name: "classic", err: ErrNoRoute},
		&fakeSource{name: "other", err: errors.New("boom")},
	}, time.Second, time.Second, nil)
	req := sendReq
	req.Account = "GACCOUNTMUSTNOTBELOGGED"
	_, err := r.Quote(context.Background(), req)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "level=WARN")
	assert.Contains(t, out, "source=lifi reason=rate_limited")
	assert.Contains(t, out, "source=other reason=error error=boom")
	assert.NotContains(t, out, "source=classic")
	assert.NotContains(t, out, req.Account)
}

func TestRouterQuoteSourcePanics(t *testing.T) {
	t.Parallel()
	m := metrics.NewSwap(prometheus.NewRegistry())
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", panics: true},
		&fakeSource{name: "classic", route: route("classic", "100", "17.9")},
	}, time.Second, time.Second, m)
	res, err := r.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	require.Len(t, res.Routes, 1)
	assert.Equal(t, []types.SwapUnavailable{{Source: "lifi", Reason: "error"}}, res.Unavailable)
	assert.Equal(t, 1.0, testutil.ToFloat64(m.SourceRequests.WithLabelValues("lifi", "quote", "error")))
}

func TestRouterBuildSourcePanics(t *testing.T) {
	t.Parallel()
	m := metrics.NewSwap(prometheus.NewRegistry())
	r := NewRouter([]Source{&fakeSource{name: "lifi", panics: true}}, time.Second, time.Second, m)
	_, err := r.Build(context.Background(), types.SwapBuildRequest{Network: types.PUBLIC, Source: "lifi"})
	require.Error(t, err)
	assert.Equal(t, "error", Reason(err))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.SourceRequests.WithLabelValues("lifi", "build", "error")))
}

func TestRouterDiscardsQuoteThatSucceedsAfterDeadline(t *testing.T) {
	t.Parallel()
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", lateOK: true, route: route("lifi", "100", "99")},
		&fakeSource{name: "classic", route: route("classic", "100", "17.9")},
	}, 20*time.Millisecond, time.Second, nil)
	res, err := r.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	require.Len(t, res.Routes, 1)
	assert.Equal(t, "classic", res.Routes[0].Source)
	assert.Equal(t, []types.SwapUnavailable{{Source: "lifi", Reason: "timeout"}}, res.Unavailable)
}

func TestRouterBuildUnsupportedNetworkDoesNotBuild(t *testing.T) {
	t.Parallel()
	src := &fakeSource{name: "lifi", supports: func(req types.SwapQuoteRequest) bool { return req.Network == types.PUBLIC }, buildXDR: "x"}
	r := NewRouter([]Source{src}, time.Second, time.Second, nil)
	_, err := r.Build(context.Background(), types.SwapBuildRequest{Network: types.TESTNET, Source: "lifi"})
	assert.ErrorIs(t, err, ErrBadRequest)
	assert.Zero(t, src.builds)
}

// A source that cannot serve the pair is not asked, so it does not dilute the
// all-failed and all-bad-request rules on the pairs only LI.FI can route.
func TestRouterPairOnlyOneSourceServes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		lifiErr error
		wantErr error
		reason  string
	}{
		{"all failed", errors.New("boom"), ErrAllSourcesFailed, "error"},
		{"all bad request", fmt.Errorf("%w: amount", ErrBadRequest), ErrBadRequest, "bad_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			horizon := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("Horizon was asked for a Soroban pair: %s", r.URL)
			}))
			t.Cleanup(horizon.Close)
			m := metrics.NewSwap(prometheus.NewRegistry())
			classic := NewClassicSource(ClassicConfig{HorizonURLs: map[string]string{types.PUBLIC: horizon.URL}}, nil)
			r := NewRouter([]Source{classic, &fakeSource{name: "lifi", err: tc.lifiErr}}, time.Second, time.Second, m)

			req := sendReq
			req.FromAsset = "CBI7UCH5KGSVQRO5H4SUCZUTZABCITZLRHQQZTWL2TK4RZ72TAR6IHRV"
			res, err := r.Quote(context.Background(), req)
			assert.ErrorIs(t, err, tc.wantErr)
			assert.Equal(t, []types.SwapUnavailable{{Source: "classic", Reason: "unsupported"}, {Source: "lifi", Reason: tc.reason}}, res.Unavailable)
			assert.Equal(t, 1, testutil.CollectAndCount(m.SourceRequests), "only the asked source is recorded")
		})
	}
}
