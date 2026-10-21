package swap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

type fakeSource struct {
	name     string
	networks map[string]bool
	route    *types.SwapRoute
	err      error
	delay    time.Duration
	buildXDR string
	buildErr error
	calls    int
}

func (f *fakeSource) Name() string           { return f.name }
func (f *fakeSource) Supports(n string) bool { return f.networks == nil || f.networks[n] }
func (f *fakeSource) BuildTx(context.Context, types.SwapBuildRequest) (string, error) {
	return f.buildXDR, f.buildErr
}
func (f *fakeSource) Quote(ctx context.Context, _ types.SwapQuoteRequest) (*types.SwapRoute, error) {
	f.calls++
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
	return &types.SwapRoute{Source: src, FromAmount: from, ToAmount: to,
		Paths: []types.SwapPath{{Share: "1", Hops: []types.SwapHop{{Venue: "stellar-dex", From: "XLM", To: "USDC:" + usdcIssuer}}}}}
}

var sendReq = types.SwapQuoteRequest{Network: types.PUBLIC, Account: "GA…", Mode: types.SwapModeSend,
	FromAsset: "XLM", ToAsset: "USDC:" + usdcIssuer, Amount: "100"}

func TestRouterRanksSendByMostOut(t *testing.T) {
	t.Parallel()
	m := metrics.NewSwap(prometheus.NewRegistry())
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", route: route("lifi", "100", "17.80")},
		&fakeSource{name: "classic", route: route("classic", "100", "17.90")},
	}, time.Second, m)
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
	}, time.Second, nil)
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
	}, time.Second, nil)
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
	}, time.Second, nil)
	res, err := r.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	assert.Empty(t, res.Routes)
	assert.Len(t, res.Unavailable, 2)
}

func TestRouterAllErroringIsAllSourcesFailed(t *testing.T) {
	t.Parallel()
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", err: errors.New("boom")},
		&fakeSource{name: "classic", err: errors.New("boom")},
	}, time.Second, nil)
	_, err := r.Quote(context.Background(), sendReq)
	assert.ErrorIs(t, err, ErrAllSourcesFailed)
}

func TestRouterUnsupportedNetworkAndMode(t *testing.T) {
	t.Parallel()
	lifi := &fakeSource{name: "lifi", networks: map[string]bool{types.PUBLIC: true}, err: ErrUnsupportedMode}
	r := NewRouter([]Source{lifi, &fakeSource{name: "classic", route: route("classic", "1", "1")}}, time.Second, nil)

	req := sendReq
	req.Network = types.TESTNET
	res, err := r.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, []types.SwapUnavailable{{Source: "lifi", Reason: "unsupported_network"}}, res.Unavailable)
	assert.Equal(t, 0, lifi.calls)

	req = sendReq
	req.Mode = types.SwapModeReceive
	res, err = r.Quote(context.Background(), req)
	require.NoError(t, err)
	assert.Empty(t, res.Unavailable) // unsupported mode is omitted
}

// A hung source doesn't hold the response past the timeout.
func TestRouterHungSourceTimesOut(t *testing.T) {
	t.Parallel()
	r := NewRouter([]Source{
		&fakeSource{name: "lifi", delay: time.Hour},
		&fakeSource{name: "classic", route: route("classic", "100", "17.9")},
	}, 50*time.Millisecond, nil)
	start := time.Now()
	res, err := r.Quote(context.Background(), sendReq)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second)
	require.Len(t, res.Routes, 1)
	assert.Equal(t, []types.SwapUnavailable{{Source: "lifi", Reason: "timeout"}}, res.Unavailable)
}
