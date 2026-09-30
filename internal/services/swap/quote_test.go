package swap

import (
	"context"
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

type fakeSwapSource struct {
	name      string
	cand      *candidate
	quoteErr  error
	wait      time.Duration
	inputWait time.Duration
	input     *big.Int
	inputErr  error
	// quoted records the request Quote last received.
	quoted types.SwapQuoteRequest
	// gaveUp is set when Quote saw its context end while it waited.
	gaveUp atomic.Bool
}

func (f *fakeSwapSource) QuoteInput(ctx context.Context, _ types.SwapQuoteRequest) (*big.Int, error) {
	if f.inputWait > 0 {
		select {
		case <-time.After(f.inputWait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.input, f.inputErr
}

func (f *fakeSwapSource) Name() string { return f.name }
func (f *fakeSwapSource) Quote(ctx context.Context, req types.SwapQuoteRequest) (*candidate, error) {
	f.quoted = req
	if f.wait > 0 {
		select {
		case <-time.After(f.wait):
		case <-ctx.Done():
			f.gaveUp.Store(true)
			return nil, ctx.Err()
		}
	}
	return f.cand, f.quoteErr
}

func newCandidate(source string, dest int64) *candidate {
	return &candidate{
		Source:        source,
		DestAmount:    big.NewInt(dest),
		DestAmountMin: big.NewInt(dest * 99 / 100),
		DestDecimals:  7,
	}
}

var swapReq = types.SwapQuoteRequest{Network: types.PUBLIC, SourceAmount: "100", SourceDecimals: 7, SlippagePercent: 1}

func TestSwapQuote_Name(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "swap-quote", newQuoteService(nil, time.Second, nil).Name())
}

func TestGetBestQuote_RecordsTheCallAndCountsNoRouteAsAnAnswer(t *testing.T) {
	t.Parallel()
	m := metrics.NewMetrics(prometheus.NewRegistry())
	svc := newQuoteService([]source{
		&fakeSwapSource{name: "horizon", quoteErr: types.ErrSwapNoRoute},
	}, time.Second, m.Service)

	_, err := svc.GetBestQuote(context.Background(), swapReq)
	require.ErrorIs(t, err, types.ErrSwapNoRoute)

	assert.InDelta(t, 1, testutil.ToFloat64(m.Service.CallsTotal.WithLabelValues("swap-quote", "GetBestQuote", "PUBLIC")), 0)
	assert.Zero(t, testutil.CollectAndCount(m.Service.ErrorsTotal), "no route is not a failure")
}

func TestGetBestQuote_HigherOutputWins(t *testing.T) {
	t.Parallel()
	svc := newQuoteService([]source{
		&fakeSwapSource{name: "horizon", cand: newCandidate("horizon", 50_0000000)},
		&fakeSwapSource{name: "xoxno", cand: newCandidate("xoxno", 51_0000000)},
	}, time.Second, nil)

	q, err := svc.GetBestQuote(context.Background(), swapReq)
	require.NoError(t, err)
	assert.Equal(t, "xoxno", q.Source)
	assert.Equal(t, "51.0000000", q.DestinationAmount)
	assert.Equal(t, "50.4900000", q.DestinationAmountMin)
	assert.Equal(t, "0.5100000", q.ConversionRate)
	require.Len(t, q.Alternatives, 2)
	assert.False(t, q.Alternatives[0].Selected)
	assert.Equal(t, "50.0000000", q.Alternatives[0].DestinationAmount)
	assert.True(t, q.Alternatives[1].Selected)
}

func TestGetBestQuote_ReportsTheSelectedTransactionsFee(t *testing.T) {
	t.Parallel()
	x := newCandidate("xoxno", 51_0000000)
	x.NetworkFee = big.NewInt(2_250_002)
	svc := newQuoteService([]source{
		&fakeSwapSource{name: "horizon", cand: newCandidate("horizon", 50_0000000)},
		&fakeSwapSource{name: "xoxno", cand: x},
	}, time.Second, nil)

	q, err := svc.GetBestQuote(context.Background(), swapReq)
	require.NoError(t, err)
	require.NotNil(t, q.NetworkFeeXlm)
	assert.Equal(t, "0.2250002", *q.NetworkFeeXlm)

	svc = newQuoteService([]source{
		&fakeSwapSource{name: "horizon", cand: newCandidate("horizon", 52_0000000)},
		&fakeSwapSource{name: "xoxno", cand: x},
	}, time.Second, nil)
	q, err = svc.GetBestQuote(context.Background(), swapReq)
	require.NoError(t, err)
	assert.Nil(t, q.NetworkFeeXlm, "a classic route leaves the fee to the client")
}

func TestGetBestQuote_TiePrefersFirstSource(t *testing.T) {
	t.Parallel()
	svc := newQuoteService([]source{
		&fakeSwapSource{name: "horizon", cand: newCandidate("horizon", 50_0000000)},
		&fakeSwapSource{name: "xoxno", cand: newCandidate("xoxno", 50_0000000)},
	}, time.Second, nil)

	q, err := svc.GetBestQuote(context.Background(), swapReq)
	require.NoError(t, err)
	assert.Equal(t, "horizon", q.Source)
}

func TestGetBestQuote_SlowSourceIsDroppedAtItsTimeout(t *testing.T) {
	t.Parallel()
	slow := &fakeSwapSource{name: "xoxno", cand: newCandidate("xoxno", 99_0000000), wait: time.Minute}
	svc := newQuoteService([]source{
		&fakeSwapSource{name: "horizon", cand: newCandidate("horizon", 50_0000000)},
		slow,
	}, 50*time.Millisecond, nil)

	q, err := svc.GetBestQuote(context.Background(), swapReq)
	require.NoError(t, err)
	assert.Equal(t, "horizon", q.Source)
	assert.Equal(t, altTimeout, q.Alternatives[1].Error)
	assert.True(t, slow.gaveUp.Load(), "the slow source's context was ended")
}

func TestGetBestQuote_AFailedSourceDoesNotWin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		failing  int
		err      error
		wantCode string
	}{
		{"upstream failure", 0, errors.New("boom"), altUnavailable},
		{"rejected quote", 1, errInvalidQuote, altInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srcs := []*fakeSwapSource{
				{name: "horizon", cand: newCandidate("horizon", 50_0000000)},
				{name: "xoxno", cand: newCandidate("xoxno", 51_0000000)},
			}
			srcs[tc.failing].cand, srcs[tc.failing].quoteErr = nil, tc.err
			svc := newQuoteService([]source{srcs[0], srcs[1]}, time.Second, nil)

			q, err := svc.GetBestQuote(context.Background(), swapReq)
			require.NoError(t, err)
			assert.Equal(t, srcs[1-tc.failing].name, q.Source)
			assert.Equal(t, tc.wantCode, q.Alternatives[tc.failing].Error)
			assert.Empty(t, q.Alternatives[tc.failing].DestinationAmount)
		})
	}
}

func TestGetBestQuote_NoSourceAnswering(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		errs    [2]error
		noRoute bool
	}{
		{"no route and unsupported are no route", [2]error{types.ErrSwapNoRoute, errUnsupported}, true},
		{"an upstream failure is an error", [2]error{context.DeadlineExceeded, types.ErrSwapNoRoute}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newQuoteService([]source{
				&fakeSwapSource{name: "horizon", quoteErr: tc.errs[0]},
				&fakeSwapSource{name: "xoxno", quoteErr: tc.errs[1]},
			}, time.Second, nil)

			_, err := svc.GetBestQuote(context.Background(), swapReq)
			require.Error(t, err)
			assert.Equal(t, tc.noRoute, errors.Is(err, types.ErrSwapNoRoute))
		})
	}
}

func TestMinAmountOut(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "990000", minAmountOut(big.NewInt(1_000_000), 1).String())
	assert.Equal(t, "999999", minAmountOut(big.NewInt(1_000_000), 0.00019).String())
	assert.Equal(t, "99", minAmountOut(big.NewInt(100), 1).String(), "floors, never rounds up")
	// 2^127 stays exact well above float64's integer range.
	big128 := new(big.Int).Lsh(big.NewInt(1), 127)
	want := new(big.Int).Mul(big128, big.NewInt(990_000))
	want.Quo(want, big.NewInt(1_000_000))
	assert.Equal(t, want, minAmountOut(big128, 1))
}

func TestFormatAtomic(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0.0000001", formatAtomic(big.NewInt(1), 7))
	assert.Equal(t, "10.0000000", formatAtomic(big.NewInt(10_0000000), 7))
	assert.Equal(t, "42", formatAtomic(big.NewInt(42), 0))
	assert.Equal(t, "1.500000000000000000", formatAtomic(new(big.Int).Mul(big.NewInt(15), new(big.Int).Exp(big.NewInt(10), big.NewInt(17), nil)), 18))
}

var exactOutReq = types.SwapQuoteRequest{Network: types.PUBLIC, DestAmount: "5", DestDecimals: 7, SourceDecimals: 7, SlippagePercent: 1}

func TestGetBestQuote_ExactOutKeepsHealthyAnswerWhenOneSourceTimesOutInBothRounds(t *testing.T) {
	t.Parallel()
	healthy := &fakeSwapSource{name: "horizon", input: big.NewInt(100000000), cand: newCandidate("horizon", 50000000)}
	slow := &fakeSwapSource{name: "xoxno", inputWait: time.Minute, wait: time.Minute}
	svc := newQuoteService([]source{healthy, slow}, 200*time.Millisecond, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	q, err := svc.GetBestQuote(ctx, exactOutReq)
	require.NoError(t, err)
	assert.Equal(t, "horizon", q.Source)
	assert.Equal(t, "10.0000000", healthy.quoted.SourceAmount)
	assert.Equal(t, altTimeout, q.Alternatives[1].Error)
}

func TestGetBestQuote_ExactOutSizesTheInputThenQuotesForwardAtIt(t *testing.T) {
	t.Parallel()
	horizon := &fakeSwapSource{name: "horizon", input: big.NewInt(10_0587578), cand: newCandidate("horizon", 5_0000000)}
	xoxno := &fakeSwapSource{name: "xoxno", input: big.NewInt(10_0489927), cand: newCandidate("xoxno", 5_0003000)}
	svc := newQuoteService([]source{horizon, xoxno}, time.Second, nil)

	q, err := svc.GetBestQuote(context.Background(), exactOutReq)
	require.NoError(t, err)

	assert.Equal(t, "10.0489927", q.SourceAmount, "the smaller sized input")
	for _, src := range []*fakeSwapSource{horizon, xoxno} {
		assert.Equal(t, "10.0489927", src.quoted.SourceAmount, "every venue is quoted for the same input")
	}
	assert.Equal(t, "xoxno", q.Source)
	assert.Equal(t, "5.0003000", q.DestinationAmount)
}

func TestGetBestQuote_ExactOutSurvivesOneSourceFailingToSize(t *testing.T) {
	t.Parallel()
	horizon := &fakeSwapSource{name: "horizon", inputErr: errors.New("boom"), cand: newCandidate("horizon", 5_0003001)}
	xoxno := &fakeSwapSource{name: "xoxno", input: big.NewInt(10_0489927), cand: newCandidate("xoxno", 5_0003000)}
	svc := newQuoteService([]source{horizon, xoxno}, time.Second, nil)

	q, err := svc.GetBestQuote(context.Background(), exactOutReq)
	require.NoError(t, err)
	assert.Equal(t, "10.0489927", q.SourceAmount, "sized by the source that could")
	assert.Equal(t, "10.0489927", horizon.quoted.SourceAmount, "the source that could not size still competes on the same input")
	assert.Equal(t, "horizon", q.Source)
}

func TestGetBestQuote_ExactOutWithEverySourceFailingToSizeIsAnError(t *testing.T) {
	t.Parallel()
	svc := newQuoteService([]source{
		&fakeSwapSource{name: "horizon", inputErr: errors.New("boom"), cand: newCandidate("horizon", 5_0000000)},
		&fakeSwapSource{name: "xoxno", inputErr: context.DeadlineExceeded, cand: newCandidate("xoxno", 5_0003000)},
	}, time.Second, nil)

	_, err := svc.GetBestQuote(context.Background(), exactOutReq)
	require.Error(t, err)
	assert.NotErrorIs(t, err, types.ErrSwapNoRoute, "sources that failed did not answer no route")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestGetBestQuote_ExactOutWithNoRouteToSizeIsNoRoute(t *testing.T) {
	t.Parallel()
	svc := newQuoteService([]source{
		&fakeSwapSource{name: "horizon", inputErr: types.ErrSwapNoRoute},
		&fakeSwapSource{name: "xoxno", inputErr: errUnsupported},
	}, time.Second, nil)

	_, err := svc.GetBestQuote(context.Background(), exactOutReq)
	assert.ErrorIs(t, err, types.ErrSwapNoRoute)
}

func TestGetBestQuote_ForwardRequestsAreNotSized(t *testing.T) {
	t.Parallel()
	horizon := &fakeSwapSource{name: "horizon", input: big.NewInt(1), cand: newCandidate("horizon", 50_0000000)}
	svc := newQuoteService([]source{horizon}, time.Second, nil)

	q, err := svc.GetBestQuote(context.Background(), swapReq)
	require.NoError(t, err)
	assert.Equal(t, "100", q.SourceAmount, "the typed amount is echoed untouched")
	assert.Equal(t, "100", horizon.quoted.SourceAmount)
}
