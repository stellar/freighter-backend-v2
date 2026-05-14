package services

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/store"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const testIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

// recentDailyCandles builds a [ts, close] price7d-shaped slice whose last
// entry's timestamp is `now` and whose entries step back 24h each. Closes
// are passed oldest-first. Used so tests aren't tied to wall-clock dates.
func recentDailyCandles(now time.Time, closes ...float64) [][2]float64 {
	out := make([][2]float64, len(closes))
	latest := now.Unix()
	for i, c := range closes {
		ts := latest - int64(len(closes)-1-i)*86400
		out[i] = [2]float64{float64(ts), c}
	}
	return out
}

// hourlyCandlesAged builds hourly candles whose first entry's timestamp is
// `oldestAge` before `now` (truncated to the hour) and whose subsequent
// entries step forward 1h. `opens` supplies the open price for each
// candle; only Open() (index 1) is read by the service code, so other
// fields are zeroed.
func hourlyCandlesAged(now time.Time, oldestAge time.Duration, opens ...float64) []types.StellarExpertCandle {
	base := now.Truncate(time.Hour).Add(-oldestAge).Unix()
	out := make([]types.StellarExpertCandle, len(opens))
	for i, op := range opens {
		ts := float64(base + int64(i)*3600)
		out[i] = types.StellarExpertCandle{ts, op, 0, 0, op, 0, 0, 0}
	}
	return out
}

// fakeStellarExpert is a programmable stub for the StellarExpertService
// interface. Tests configure assets via Set and inspect call counts via Calls.
type fakeStellarExpert struct {
	mu              sync.Mutex
	assets          map[string]*types.StellarExpertAsset
	candles         map[string][]types.StellarExpertCandle
	candleErrs      map[string]error
	errs            map[string]error
	calls           map[string]int
	candleCalls     map[string]int
	delay           time.Duration
	concurrentInUse atomic.Int64
	maxConcurrent   atomic.Int64
}

func newFakeStellarExpert() *fakeStellarExpert {
	return &fakeStellarExpert{
		assets:      map[string]*types.StellarExpertAsset{},
		candles:     map[string][]types.StellarExpertCandle{},
		candleErrs:  map[string]error{},
		errs:        map[string]error{},
		calls:       map[string]int{},
		candleCalls: map[string]int{},
	}
}

func (f *fakeStellarExpert) Name() string { return "fake-expert" }

func (f *fakeStellarExpert) GetAsset(ctx context.Context, network, assetID string) (*types.StellarExpertAsset, error) {
	in := f.concurrentInUse.Add(1)
	for {
		cur := f.maxConcurrent.Load()
		if in <= cur || f.maxConcurrent.CompareAndSwap(cur, in) {
			break
		}
	}
	defer f.concurrentInUse.Add(-1)

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	f.mu.Lock()
	f.calls[assetID]++
	asset, ok := f.assets[assetID]
	err := f.errs[assetID]
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrAssetNotFound
	}
	return asset, nil
}

func (f *fakeStellarExpert) GetAssetCandles(ctx context.Context, network, assetID string, from, to time.Time, resolutionSec int) ([]types.StellarExpertCandle, error) {
	in := f.concurrentInUse.Add(1)
	for {
		cur := f.maxConcurrent.Load()
		if in <= cur || f.maxConcurrent.CompareAndSwap(cur, in) {
			break
		}
	}
	defer f.concurrentInUse.Add(-1)

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	f.mu.Lock()
	f.candleCalls[assetID]++
	rows, ok := f.candles[assetID]
	err := f.candleErrs[assetID]
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if !ok {
		// No candles configured: return empty so the prices service's
		// price7d fallback path is exercised.
		return nil, nil
	}
	return rows, nil
}

func (f *fakeStellarExpert) Set(assetID string, asset *types.StellarExpertAsset) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assets[assetID] = asset
}

func (f *fakeStellarExpert) SetCandles(assetID string, rows []types.StellarExpertCandle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.candles[assetID] = rows
}

func (f *fakeStellarExpert) SetCandleErr(assetID string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.candleErrs[assetID] = err
}

func (f *fakeStellarExpert) SetErr(assetID string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[assetID] = err
}

func (f *fakeStellarExpert) CallCount(assetID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[assetID]
}

func (f *fakeStellarExpert) CandleCallCount(assetID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.candleCalls[assetID]
}

func TestPrices_HappyPath_NoCache(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{
		Price:   0.16,
		Price7d: recentDailyCandles(now, 0.18, 0.17, 0.169, 0.17, 0.165, 0.161, 0.158, 0.159),
	})
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{
		Price:   1.0,
		Price7d: recentDailyCandles(now, 1.0, 1.0),
	})

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"XLM", "USDC:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)

	xlm := got["XLM"]
	require.NotNil(t, xlm)
	assert.Equal(t, "0.16", xlm.CurrentPrice)
	require.NotNil(t, xlm.PercentagePriceChange24h)
	// (0.16 - 0.158) / 0.158 * 100 = 1.265... → rounded to 1.27
	assert.Equal(t, "1.27", *xlm.PercentagePriceChange24h)

	usdc := got["USDC:"+testIssuer]
	require.NotNil(t, usdc)
	assert.Equal(t, "1", usdc.CurrentPrice)
	require.NotNil(t, usdc.PercentagePriceChange24h)
	assert.Equal(t, "0", *usdc.PercentagePriceChange24h)
}

func TestPrices_UsesCandlesWhenAvailable(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stellarExpert := newFakeStellarExpert()
	// price7d would yield 1% if used. Candles yield -10% — verify candles win.
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{
		Price:   1.10,
		Price7d: recentDailyCandles(now, 1.0, 1.089), // 1.01% if computed from price7d
	})
	// oldest open=1.222 → (1.10-1.222)/1.222*100 ≈ -9.98 → -9.98
	stellarExpert.SetCandles("USDC-"+testIssuer+"-1", hourlyCandlesAged(now, 24*time.Hour, 1.222, 1.21))

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"USDC:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)

	usdc := got["USDC:"+testIssuer]
	require.NotNil(t, usdc)
	assert.Equal(t, "1.1", usdc.CurrentPrice)
	require.NotNil(t, usdc.PercentagePriceChange24h)
	assert.Equal(t, "-9.98", *usdc.PercentagePriceChange24h)
	assert.Equal(t, 1, stellarExpert.CandleCallCount("USDC-"+testIssuer+"-1"))
}

func TestPrices_CandlesEmptyFallsBackToPrice7d(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stellarExpert := newFakeStellarExpert()
	// Candles unset on the fake → returns empty. Service must still call
	// candles and then fall back to price7d for the 24h change.
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{
		Price:   1.10,
		Price7d: recentDailyCandles(now, 1.0, 1.099),
	})

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"USDC:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)

	usdc := got["USDC:"+testIssuer]
	require.NotNil(t, usdc)
	assert.Equal(t, "1.1", usdc.CurrentPrice)
	require.NotNil(t, usdc.PercentagePriceChange24h)
	// price7d-derived: (1.10 - 1.0) / 1.0 * 100 = 10
	assert.Equal(t, "10", *usdc.PercentagePriceChange24h)
	assert.Equal(t, 1, stellarExpert.CandleCallCount("USDC-"+testIssuer+"-1"), "candles call is made even when empty; price7d drives the fallback")
}

func TestPrices_XLM_UsesCandles(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stellarExpert := newFakeStellarExpert()
	// price7d would yield ~0.63% if used; candles yield ~-2.02 — verify candles win.
	stellarExpert.Set("XLM", &types.StellarExpertAsset{
		Price:   0.16,
		Price7d: recentDailyCandles(now, 0.159, 0.16),
	})
	// oldest open=0.1633 → (0.16 - 0.1633) / 0.1633 * 100 ≈ -2.02
	stellarExpert.SetCandles("XLM", hourlyCandlesAged(now, 24*time.Hour, 0.1633, 0.1625))

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)

	xlm := got["XLM"]
	require.NotNil(t, xlm)
	assert.Equal(t, "0.16", xlm.CurrentPrice)
	require.NotNil(t, xlm.PercentagePriceChange24h)
	assert.Equal(t, "-2.02", *xlm.PercentagePriceChange24h)
	assert.Equal(t, 1, stellarExpert.CandleCallCount("XLM"))
}

func TestPrices_CandlesErrorFallsBackToPrice7d(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{
		Price: 1.0,
		// compute24hChange reads price7d[len-2], i.e. 0.95 here:
		// (1.0 - 0.95) / 0.95 * 100 ≈ 5.26
		Price7d: recentDailyCandles(now, 0.95, 0.97),
	})
	stellarExpert.SetCandleErr("USDC-"+testIssuer+"-1", errors.New("transient candles boom"))

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"USDC:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)

	usdc := got["USDC:"+testIssuer]
	require.NotNil(t, usdc)
	require.NotNil(t, usdc.PercentagePriceChange24h)
	assert.Equal(t, "5.26", *usdc.PercentagePriceChange24h)
}

// Sparse upstream data: candles return 2 buckets but the oldest is only
// 6h old. The coverage check must reject this so the service falls back
// to the price7d path rather than labeling a 6h change as "24h".
func TestPrices_SparseCandles_FallsBackToPrice7d(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{
		Price: 1.0,
		// Fresh price7d; (1.0-0.95)/0.95*100 ≈ 5.26
		Price7d: recentDailyCandles(now, 0.95, 0.97),
	})
	// Only 6h of coverage — outside [23h, 25h] from `to`.
	stellarExpert.SetCandles("USDC-"+testIssuer+"-1", hourlyCandlesAged(now, 6*time.Hour, 0.5, 0.6))

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"USDC:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)

	usdc := got["USDC:"+testIssuer]
	require.NotNil(t, usdc)
	require.NotNil(t, usdc.PercentagePriceChange24h)
	assert.Equal(t, "5.26", *usdc.PercentagePriceChange24h, "sparse candles must fall through to price7d, not produce a 6h change")
}

// When /candles is empty AND price7d's last daily candle is older than
// maxPrice7dFallbackAge, the entry is still returned with a real price but
// percentagePriceChange24h is suppressed rather than computed off stale data.
func TestPrices_StalePrice7d_SuppressesFallbackChange(t *testing.T) {
	t.Parallel()

	stalePast := time.Now().UTC().Add(-3 * 24 * time.Hour)
	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{
		Price:   1.0,
		Price7d: recentDailyCandles(stalePast, 0.95, 0.97),
	})

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"USDC:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)

	usdc := got["USDC:"+testIssuer]
	require.NotNil(t, usdc)
	assert.Equal(t, "1", usdc.CurrentPrice)
	assert.Nil(t, usdc.PercentagePriceChange24h, "stale price7d must not produce a 24h change")
}

func TestPrices_NotFound_ReturnsNull(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Price7d: [][2]float64{{1, 0.15}, {2, 0.16}}})

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"XLM", "BOGUS:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)
	assert.NotNil(t, got["XLM"])
	bogus, ok := got["BOGUS:"+testIssuer]
	assert.True(t, ok)
	assert.Nil(t, bogus)
}

func TestPrices_Malformed_ReturnsNull(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.SetErr("BAD-"+testIssuer+"-1", ErrAssetMalformed)

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"BAD:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)
	assert.Nil(t, got["BAD:"+testIssuer])
}

func TestPrices_UpstreamError_ReturnsNull(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.SetErr("XLM", errors.New("transport boom"))

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)
	xlm, ok := got["XLM"]
	assert.True(t, ok)
	assert.Nil(t, xlm)
}

func TestPrices_DedupesDuplicateTokens(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Price7d: [][2]float64{{1, 0.15}, {2, 0.16}}})

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	_, err := svc.GetPrices(context.Background(), []string{"XLM", "XLM", "XLM"}, types.PUBLIC)
	require.NoError(t, err)
	assert.Equal(t, 1, stellarExpert.CallCount("XLM"))
}

func TestPrices_RejectsUnsupportedNetwork(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	_, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.FUTURENET)
	require.Error(t, err)
}

func TestPrices_ConcurrencyCapHonored(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.delay = 50 * time.Millisecond
	tokens := make([]string, 20)
	for i := range tokens {
		// Use distinct codes (4 chars) so each canonical id is unique.
		code := []byte{'A', 'A', 'A', byte('A' + i)}
		stellarExpertID := string(code) + "-" + testIssuer + "-1"
		stellarExpert.Set(stellarExpertID, &types.StellarExpertAsset{Price: 1.0, Price7d: [][2]float64{{1, 1}, {2, 1}}})
		tokens[i] = string(code) + ":" + testIssuer
	}

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{MaxConcurrent: 4}, nil, nil)
	got, err := svc.GetPrices(context.Background(), tokens, types.PUBLIC)
	require.NoError(t, err)
	assert.Len(t, got, 20)
	// MaxConcurrent caps tokens-in-flight; each token issues GetAsset and
	// GetAssetCandles in parallel, so the observed HTTP-level concurrency
	// ceiling is 2× MaxConcurrent.
	assert.LessOrEqual(t, stellarExpert.maxConcurrent.Load(), int64(8),
		"expected at most 8 concurrent upstream calls (2× workers), observed %d", stellarExpert.maxConcurrent.Load())
}

func TestPrices_MissFetchTimeoutReturnsBestEffortWithoutError(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.delay = 100 * time.Millisecond
	tokens := []string{"XLM", "USDC:" + testIssuer}

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{
		MaxConcurrent:    1,
		MissFetchTimeout: 10 * time.Millisecond,
	}, nil, nil)
	got, err := svc.GetPrices(context.Background(), tokens, types.PUBLIC)
	require.NoError(t, err)
	require.Len(t, got, len(tokens))
	assert.Nil(t, got["XLM"])
	assert.Nil(t, got["USDC:"+testIssuer])
}

func TestPrices_PreservesPartialOnContextCancel(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.delay = 100 * time.Millisecond
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Price7d: [][2]float64{{1, 0.15}, {2, 0.16}}})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{MaxConcurrent: 1}, nil, nil)
	got, err := svc.GetPrices(ctx, []string{"XLM"}, types.PUBLIC)
	// errgroup surfaces ctx.Err() but we still receive the (possibly empty)
	// partial result map.
	if err != nil {
		assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))
	}
	assert.NotNil(t, got)
}

func TestCachedPriceEntryFreshness(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	entry := cachedPriceEntry{
		CurrentPrice: "1",
		FetchedAt:    now.Add(-20 * time.Second).Format(time.RFC3339Nano),
	}
	assert.Equal(t, cacheFresh, entry.freshness(now, 30*time.Second, 2*time.Minute))

	entry.FetchedAt = now.Add(-45 * time.Second).Format(time.RFC3339Nano)
	assert.Equal(t, cacheStale, entry.freshness(now, 30*time.Second, 2*time.Minute))

	entry.FetchedAt = now.Add(-3 * time.Minute).Format(time.RFC3339Nano)
	assert.Equal(t, cacheMissing, entry.freshness(now, 30*time.Second, 2*time.Minute))

	entry.FetchedAt = "not-a-time"
	assert.Equal(t, cacheMissing, entry.freshness(now, 30*time.Second, 2*time.Minute))
}

func TestCompleteMissingResultsUsesStaleFallback(t *testing.T) {
	t.Parallel()

	tokens := []string{"XLM", "USDC:" + testIssuer, "BOGUS:" + testIssuer}
	result := map[string]*types.PriceEntry{
		"XLM": nil,
	}
	staleFallback := map[string]*types.PriceEntry{
		"USDC:" + testIssuer: {CurrentPrice: "1", PercentagePriceChange24h: ptrStr("0")},
	}

	staleServed := completeMissingResults(tokens, result, staleFallback)
	require.Len(t, result, len(tokens))
	assert.Nil(t, result["XLM"])
	require.NotNil(t, result["USDC:"+testIssuer])
	assert.Equal(t, "1", result["USDC:"+testIssuer].CurrentPrice)
	assert.Nil(t, result["BOGUS:"+testIssuer])
	assert.Equal(t, 1, staleServed, "one token took the stale fallback")
}

func TestCompute24hChange(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	fresh := func(closes ...float64) [][2]float64 { return recentDailyCandles(now, closes...) }

	cases := []struct {
		name    string
		current float64
		candles [][2]float64
		want    *string
	}{
		{"no candles", 1.0, nil, nil},
		{"single candle", 1.0, fresh(1), nil},
		{"zero prior denom", 1.0, fresh(0, 0.5), nil},
		{"+10% rise vs prior", 1.10, fresh(1.0, 1.05), ptrStr("10")},
		{"-25% drop vs prior", 0.90, fresh(1.2, 1.0), ptrStr("-25")},
		{"8 candles uses index len-2", 0.16, fresh(0.18, 0.17, 0.169, 0.17, 0.165, 0.161, 0.158, 0.159), ptrStr("1.27")},
		{"rounding to 2 decimals", 1.0001, fresh(1.0, 0.999), ptrStr("0.01")},
		{"negative zero collapses to 0", 0.999999, fresh(1.0, 1.0), ptrStr("0")},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := compute24hChange(tc.current, tc.candles, now)
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tc.want, *got)
		})
	}
}

func TestCompute24hChange_StalePrice7d_ReturnsNil(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	// Last candle is 48h old — beyond the 26h freshness threshold.
	stale := recentDailyCandles(now.Add(-48*time.Hour), 0.95, 0.97)
	assert.Nil(t, compute24hChange(1.0, stale, now))
}

func TestCompute24hChange_FreshPrice7d_ComputesChange(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	// Last candle 12h old — well within the 26h threshold.
	fresh := recentDailyCandles(now.Add(-12*time.Hour), 0.95, 0.97)
	got := compute24hChange(1.0, fresh, now)
	require.NotNil(t, got)
	// (1.0 - 0.95) / 0.95 * 100 ≈ 5.26
	assert.Equal(t, "5.26", *got)
}

func TestFormatPrice(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0.15968791272173", formatPrice(0.15968791272173))
	assert.Equal(t, "1", formatPrice(1.0))
	assert.Equal(t, "0.0000001", formatPrice(0.0000001)) // no scientific notation
	assert.Equal(t, "0", formatPrice(0))
}

func ptrStr(s string) *string { return &s }

func TestPrices_CacheOutcomes_NilRedisCountsAllAsMisses(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Price7d: [][2]float64{{1, 0.15}, {2, 0.16}}})
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 1.0, Price7d: [][2]float64{{1, 1}, {2, 1}}})

	reg := prometheus.NewRegistry()
	pm := metrics.NewPrices(reg)
	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, pm)

	_, err := svc.GetPrices(context.Background(), []string{"XLM", "USDC:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)

	assert.Equal(t, float64(2), testutil.ToFloat64(pm.CacheOutcomes.WithLabelValues(types.PUBLIC, "miss")))
	assert.Equal(t, float64(0), testutil.ToFloat64(pm.CacheOutcomes.WithLabelValues(types.PUBLIC, "hit_fresh")))
	assert.Equal(t, float64(0), testutil.ToFloat64(pm.CacheOutcomes.WithLabelValues(types.PUBLIC, "hit_stale")))
}

func TestPrices_MissBudgetExhausted_EmitsMetric(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.delay = 100 * time.Millisecond
	tokens := []string{"XLM", "USDC:" + testIssuer}

	reg := prometheus.NewRegistry()
	pm := metrics.NewPrices(reg)
	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{
		MaxConcurrent:    1,
		MissFetchTimeout: 10 * time.Millisecond,
	}, nil, pm)

	_, err := svc.GetPrices(context.Background(), tokens, types.PUBLIC)
	require.NoError(t, err)

	assert.Equal(t, float64(1), testutil.ToFloat64(pm.MissBudgetExhausted.WithLabelValues(types.PUBLIC)))
}

// Pointing at an unreachable port makes MGetJSON return an error, which the
// service swallows and falls through to upstream — but it should still bump
// the redis_errors{op=mget} counter so operators see the cache bypass.
func TestPrices_RedisErrors_MGetUnreachable_Increments(t *testing.T) {
	t.Parallel()

	redisStore := store.NewRedisStore("localhost", 1, "") // port 1 = no listener
	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Price7d: [][2]float64{{1, 0.15}, {2, 0.16}}})

	reg := prometheus.NewRegistry()
	pm := metrics.NewPrices(reg)
	svc := NewPricesService(stellarExpert, redisStore, PricesServiceConfig{
		MissFetchTimeout: 250 * time.Millisecond,
	}, nil, pm)

	_, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, testutil.ToFloat64(pm.RedisErrors.WithLabelValues("mget")), float64(1))
}

func TestPrices_NilMetrics_NoOps(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Price7d: [][2]float64{{1, 0.15}, {2, 0.16}}})

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	_, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)
}
