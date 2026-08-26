package services

import (
	"context"
	"encoding/json"
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

// candlesAged builds 15-minute candles whose first entry's timestamp is
// `oldestAge` before `now` (truncated to the hour, which is also on a
// 15-minute boundary) and whose subsequent entries step forward 900s.
// `closes` supplies the close price (index 4) for each candle — the value
// the 24h formula anchors on. The open (index 1) is deliberately set to a
// different value so a regression back to the pre-D8 open anchor cannot
// pass; other fields are zeroed.
func candlesAged(now time.Time, oldestAge time.Duration, closes ...float64) []types.StellarExpertCandle {
	base := now.Truncate(time.Hour).Add(-oldestAge).Unix()
	out := make([]types.StellarExpertCandle, len(closes))
	for i, cl := range closes {
		ts := float64(base + int64(i)*900)
		out[i] = types.StellarExpertCandle{ts, cl * 1.5, 0, 0, cl, 0, 0, 0}
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
	candleRes       map[string]int
	candleFrom      map[string]time.Time
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
		candleRes:   map[string]int{},
		candleFrom:  map[string]time.Time{},
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
	f.candleRes[assetID] = resolutionSec
	f.candleFrom[assetID] = from
	rows, ok := f.candles[assetID]
	err := f.candleErrs[assetID]
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if !ok {
		// No candles configured: return empty so the prices service's
		// empty-candles → null-change path is exercised.
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

// LastCandleResolution reports the resolutionSec of the most recent
// GetAssetCandles call for assetID (0 when never called).
func (f *fakeStellarExpert) LastCandleResolution(assetID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.candleRes[assetID]
}

// LastCandleFrom reports the `from` of the most recent GetAssetCandles call
// for assetID (zero time when never called).
func (f *fakeStellarExpert) LastCandleFrom(assetID string) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.candleFrom[assetID]
}

// D8 alignment (§4.2): the 24h delta anchors on the first candle's CLOSE (the
// first plotted chart point), not its open, and the candles request uses the
// chart's 1D resolution (900s). Open and close differ in this fixture so an
// accidental revert to the open anchor fails loudly.
func TestPrices_Change24h_AnchorsOnFirstCloseAt15mResolution(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})
	// first candle: open=0.150, close=0.158. Close anchor:
	// (0.16 - 0.158) / 0.158 * 100 = 1.2658... → 1.27.
	// The old open anchor would give (0.16 - 0.150) / 0.150 * 100 = 6.67.
	rows := candlesAged(now, 24*time.Hour, 0.158, 0.159)
	rows[0][1] = 0.150 // open ≠ close
	stellarExpert.SetCandles("XLM", rows)

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)

	xlm := got["XLM"]
	require.NotNil(t, xlm)
	require.NotNil(t, xlm.PercentagePriceChange24h)
	assert.Equal(t, "1.27", *xlm.PercentagePriceChange24h)
	assert.Equal(t, 900, stellarExpert.LastCandleResolution("XLM"),
		"24h candles must be requested at the chart's 1D resolution (900s)")
}

func TestPrices_HappyPath_NoCache(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})
	// oldest close=0.158 → (0.16-0.158)/0.158*100 ≈ 1.27
	stellarExpert.SetCandles("XLM", candlesAged(now, 24*time.Hour, 0.158, 0.159))
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 1.0})
	// flat → 0%
	stellarExpert.SetCandles("USDC-"+testIssuer+"-1", candlesAged(now, 24*time.Hour, 1.0, 1.0))

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
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 1.10})
	// oldest close=1.222 → (1.10-1.222)/1.222*100 ≈ -9.98
	stellarExpert.SetCandles("USDC-"+testIssuer+"-1", candlesAged(now, 24*time.Hour, 1.222, 1.21))

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

// When /candles returns empty (no recent trades), the price is still served
// but the 24h change is null — we no longer synthesize one from price7d.
func TestPrices_CandlesEmpty_NoChange(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	// Candles unset on the fake → returns empty.
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 1.10})

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"USDC:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)

	usdc := got["USDC:"+testIssuer]
	require.NotNil(t, usdc)
	assert.Equal(t, "1.1", usdc.CurrentPrice)
	assert.Nil(t, usdc.PercentagePriceChange24h, "empty candles → null 24h change")
	assert.Equal(t, 1, stellarExpert.CandleCallCount("USDC-"+testIssuer+"-1"), "candles call is still made")
}

func TestPrices_XLM_UsesCandles(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})
	// oldest close=0.1633 → (0.16 - 0.1633) / 0.1633 * 100 ≈ -2.02
	stellarExpert.SetCandles("XLM", candlesAged(now, 24*time.Hour, 0.1633, 0.1625))

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

// A transient candles error leaves the price intact but yields a null 24h
// change — there is no price7d fallback.
func TestPrices_CandlesError_NoChange(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 1.0})
	stellarExpert.SetCandleErr("USDC-"+testIssuer+"-1", errors.New("transient candles boom"))

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"USDC:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)

	usdc := got["USDC:"+testIssuer]
	require.NotNil(t, usdc)
	assert.Equal(t, "1", usdc.CurrentPrice)
	assert.Nil(t, usdc.PercentagePriceChange24h, "candles error → null 24h change")
}

// Sparse upstream data: candles return 2 buckets but the oldest is only
// 6h old. The coverage check rejects this, so the 24h change is null rather
// than a 6h change mislabeled as "24h".
func TestPrices_SparseCandles_NoChange(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 1.0})
	// Only 6h of coverage — outside [23h, 25h] from `to`.
	stellarExpert.SetCandles("USDC-"+testIssuer+"-1", candlesAged(now, 6*time.Hour, 0.5, 0.6))

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"USDC:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)

	usdc := got["USDC:"+testIssuer]
	require.NotNil(t, usdc)
	assert.Equal(t, "1", usdc.CurrentPrice)
	assert.Nil(t, usdc.PercentagePriceChange24h, "sparse candles → null, not a 6h change")
}

func TestPrices_NotFound_ReturnsNull(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})

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

// A zero price is unpriceable and must serialize as null, not a manufactured
// "0". This covers both Stellar Expert omitting the `price` field for a known
// but illiquid asset (JSON absence decodes to 0) and a genuine reported 0 —
// the two are indistinguishable and treated identically.
func TestPrices_ZeroPrice_ReturnsNull(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("USDyc-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 0})

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	got, err := svc.GetPrices(context.Background(), []string{"USDyc:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)
	entry, ok := got["USDyc:"+testIssuer]
	assert.True(t, ok)
	assert.Nil(t, entry, "zero price → null, not \"0\"")
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
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})

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
		stellarExpert.Set(stellarExpertID, &types.StellarExpertAsset{Price: 1.0})
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

func TestPrices_CoalescesConcurrentFetches(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	// A delay wide enough that all goroutines are in-flight at DoChan before
	// the shared fetch completes, so singleflight coalesces them.
	stellarExpert.delay = 50 * time.Millisecond
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)

	const n = 10
	var wg sync.WaitGroup
	results := make([]*types.PriceEntry, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
			errs[i] = err
			if err == nil {
				results[i] = got["XLM"]
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 1, stellarExpert.CallCount("XLM"),
		"concurrent fetches for the same token should coalesce to a single upstream call")
	for i := range results {
		require.NoError(t, errs[i])
		require.NotNil(t, results[i], "every caller receives the shared result")
		assert.Equal(t, "0.16", results[i].CurrentPrice)
	}
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
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})

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

func TestCompleteMissingResultsFillsNil(t *testing.T) {
	t.Parallel()

	tokens := []string{"XLM", "USDC:" + testIssuer, "BOGUS:" + testIssuer}
	result := map[string]*types.PriceEntry{
		"USDC:" + testIssuer: {CurrentPrice: "1", PercentagePriceChange24h: ptrStr("0")},
	}

	completeMissingResults(tokens, result)
	require.Len(t, result, len(tokens))
	assert.Nil(t, result["XLM"])
	assert.Nil(t, result["BOGUS:"+testIssuer])
	require.NotNil(t, result["USDC:"+testIssuer])
	assert.Equal(t, "1", result["USDC:"+testIssuer].CurrentPrice)
}

func TestFormatPrice(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0.15968791272173", formatPrice(0.15968791272173))
	assert.Equal(t, "1", formatPrice(1.0))
	assert.Equal(t, "0.0000001", formatPrice(0.0000001)) // no scientific notation
	assert.Equal(t, "0", formatPrice(0))
}

func ptrStr(s string) *string { return &s }

// fakeJSONCache is an in-memory JSONCache so cache round-trips (positive and
// negative entries, TTL choice) are testable without a Redis listener.
type fakeJSONCache struct {
	mu      sync.Mutex
	entries map[string][]byte
	ttls    map[string]time.Duration
}

func newFakeJSONCache() *fakeJSONCache {
	return &fakeJSONCache{entries: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (f *fakeJSONCache) MGetJSON(ctx context.Context, keys []string, makeDest func() any) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		raw, ok := f.entries[k]
		if !ok {
			continue
		}
		dest := makeDest()
		if err := json.Unmarshal(raw, dest); err != nil {
			continue
		}
		out[k] = dest
	}
	return out, nil
}

func (f *fakeJSONCache) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[key] = encoded
	f.ttls[key] = ttl
	return nil
}

func (f *fakeJSONCache) TTL(key string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ttls[key]
}

// Unpriceable tokens are negatively cached (§6.2): once clients stop
// filtering custom tokens, an unpriced token in a balance list would
// otherwise hit upstream on every 30s poll, uncacheably. A second request
// within the negative TTL must make zero upstream calls and still serve an
// explicit null.
func TestPrices_NullEntryNegativelyCached(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert() // BOGUS unknown → ErrAssetNotFound
	cache := newFakeJSONCache()
	svc := NewPricesService(stellarExpert, cache, PricesServiceConfig{}, nil, nil)

	got, err := svc.GetPrices(context.Background(), []string{"BOGUS:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)
	entry, ok := got["BOGUS:"+testIssuer]
	require.True(t, ok)
	assert.Nil(t, entry)
	assert.Equal(t, 1, stellarExpert.CallCount("BOGUS-"+testIssuer+"-2"))

	// The null entry is cached at the flat 15m negative TTL, not the 30s
	// positive TTL.
	assert.Equal(t, 15*time.Minute, cache.TTL("prices:v1:public:BOGUS:"+testIssuer))

	got, err = svc.GetPrices(context.Background(), []string{"BOGUS:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)
	entry, ok = got["BOGUS:"+testIssuer]
	require.True(t, ok)
	assert.Nil(t, entry, "negative cache hit must still serve an explicit null")
	assert.Equal(t, 1, stellarExpert.CallCount("BOGUS-"+testIssuer+"-2"),
		"second request within the negative TTL must make zero upstream calls")
}

// A transient upstream failure is NOT authoritative and must not be
// negatively cached — the next request should retry upstream.
func TestPrices_TransientErrorNotNegativelyCached(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.SetErr("XLM", errors.New("transport boom"))
	cache := newFakeJSONCache()
	svc := NewPricesService(stellarExpert, cache, PricesServiceConfig{}, nil, nil)

	_, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)
	assert.Equal(t, 1, stellarExpert.CallCount("XLM"))

	_, err = svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)
	assert.Equal(t, 2, stellarExpert.CallCount("XLM"), "transient failures must retry upstream")
}

// Positive entries keep the configured (30s default) TTL and round-trip
// through the cache.
func TestPrices_PositiveEntryCachedAtConfiguredTTL(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})
	stellarExpert.SetCandles("XLM", candlesAged(now, 24*time.Hour, 0.158, 0.159))
	cache := newFakeJSONCache()
	svc := NewPricesService(stellarExpert, cache, PricesServiceConfig{CacheTTL: 45 * time.Second}, nil, nil)

	_, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)
	assert.Equal(t, 45*time.Second, cache.TTL("prices:v1:public:XLM"))

	got, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)
	require.NotNil(t, got["XLM"])
	assert.Equal(t, "0.16", got["XLM"].CurrentPrice)
	assert.Equal(t, 1, stellarExpert.CallCount("XLM"), "cache hit must not refetch")
}

func TestPrices_CacheOutcomes_NilRedisCountsAllAsMisses(t *testing.T) {
	t.Parallel()

	stellarExpert := newFakeStellarExpert()
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})
	stellarExpert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 1.0})

	reg := prometheus.NewRegistry()
	pm := metrics.NewPrices(reg)
	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, pm)

	_, err := svc.GetPrices(context.Background(), []string{"XLM", "USDC:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)

	assert.Equal(t, float64(2), testutil.ToFloat64(pm.CacheOutcomes.WithLabelValues(types.PUBLIC, "miss")))
	assert.Equal(t, float64(0), testutil.ToFloat64(pm.CacheOutcomes.WithLabelValues(types.PUBLIC, "hit")))
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
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})

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
	stellarExpert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})

	svc := NewPricesService(stellarExpert, nil, PricesServiceConfig{}, nil, nil)
	_, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)
}
