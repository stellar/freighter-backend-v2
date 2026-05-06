package services

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

const testIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

// fakeStellarExpert is a programmable stub for the StellarExpertService
// interface. Tests configure assets via Set and inspect call counts via Calls.
type fakeStellarExpert struct {
	mu              sync.Mutex
	assets          map[string]*types.StellarExpertAsset
	errs            map[string]error
	calls           map[string]int
	delay           time.Duration
	concurrentInUse atomic.Int64
	maxConcurrent   atomic.Int64
}

func newFakeStellarExpert() *fakeStellarExpert {
	return &fakeStellarExpert{
		assets: map[string]*types.StellarExpertAsset{},
		errs:   map[string]error{},
		calls:  map[string]int{},
	}
}

func (f *fakeStellarExpert) Name() string { return "fake-expert" }

func (f *fakeStellarExpert) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	return types.GetHealthResponse{Status: types.StatusHealthy}, nil
}

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

func (f *fakeStellarExpert) Set(assetID string, asset *types.StellarExpertAsset) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assets[assetID] = asset
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

func TestPrices_HappyPath_NoCache(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{
		Price: 0.16,
		Price7d: [][2]float64{
			{1776902400, 0.18},
			{1776988800, 0.17},
			{1777075200, 0.169},
			{1777161600, 0.17},
			{1777248000, 0.165},
			{1777334400, 0.161},
			{1777420800, 0.158}, // -2 candle (~24h ago)
			{1777507200, 0.159}, // last
		},
	})
	expert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{
		Price: 1.0,
		Price7d: [][2]float64{
			{1, 1.0},
			{2, 1.0},
		},
	})

	svc := NewPricesService(expert, nil, PricesServiceConfig{}, nil)
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

func TestPrices_NotFound_ReturnsNull(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Price7d: [][2]float64{{1, 0.15}, {2, 0.16}}})

	svc := NewPricesService(expert, nil, PricesServiceConfig{}, nil)
	got, err := svc.GetPrices(context.Background(), []string{"XLM", "BOGUS:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)
	assert.NotNil(t, got["XLM"])
	bogus, ok := got["BOGUS:"+testIssuer]
	assert.True(t, ok)
	assert.Nil(t, bogus)
}

func TestPrices_Malformed_ReturnsNull(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.SetErr("BAD-"+testIssuer+"-1", ErrAssetMalformed)

	svc := NewPricesService(expert, nil, PricesServiceConfig{}, nil)
	got, err := svc.GetPrices(context.Background(), []string{"BAD:" + testIssuer}, types.PUBLIC)
	require.NoError(t, err)
	assert.Nil(t, got["BAD:"+testIssuer])
}

func TestPrices_UpstreamError_ReturnsNull(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.SetErr("XLM", errors.New("transport boom"))

	svc := NewPricesService(expert, nil, PricesServiceConfig{}, nil)
	got, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)
	xlm, ok := got["XLM"]
	assert.True(t, ok)
	assert.Nil(t, xlm)
}

func TestPrices_DedupesDuplicateTokens(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Price7d: [][2]float64{{1, 0.15}, {2, 0.16}}})

	svc := NewPricesService(expert, nil, PricesServiceConfig{}, nil)
	_, err := svc.GetPrices(context.Background(), []string{"XLM", "XLM", "XLM"}, types.PUBLIC)
	require.NoError(t, err)
	assert.Equal(t, 1, expert.CallCount("XLM"))
}

func TestPrices_RejectsUnsupportedNetwork(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	svc := NewPricesService(expert, nil, PricesServiceConfig{}, nil)
	_, err := svc.GetPrices(context.Background(), []string{"XLM"}, types.FUTURENET)
	require.Error(t, err)
}

func TestPrices_ConcurrencyCapHonored(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.delay = 50 * time.Millisecond
	tokens := make([]string, 20)
	for i := range tokens {
		// Use distinct codes (4 chars) so each canonical id is unique.
		code := []byte{'A', 'A', 'A', byte('A' + i)}
		expertID := string(code) + "-" + testIssuer + "-1"
		expert.Set(expertID, &types.StellarExpertAsset{Price: 1.0, Price7d: [][2]float64{{1, 1}, {2, 1}}})
		tokens[i] = string(code) + ":" + testIssuer
	}

	svc := NewPricesService(expert, nil, PricesServiceConfig{MaxConcurrent: 4}, nil)
	got, err := svc.GetPrices(context.Background(), tokens, types.PUBLIC)
	require.NoError(t, err)
	assert.Len(t, got, 20)
	assert.LessOrEqual(t, expert.maxConcurrent.Load(), int64(4),
		"expected at most 4 concurrent upstream calls, observed %d", expert.maxConcurrent.Load())
}

func TestPrices_MissFetchTimeoutReturnsBestEffortWithoutError(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.delay = 100 * time.Millisecond
	tokens := []string{"XLM", "USDC:" + testIssuer}

	svc := NewPricesService(expert, nil, PricesServiceConfig{
		MaxConcurrent:    1,
		MissFetchTimeout: 10 * time.Millisecond,
	}, nil)
	got, err := svc.GetPrices(context.Background(), tokens, types.PUBLIC)
	require.NoError(t, err)
	require.Len(t, got, len(tokens))
	assert.Nil(t, got["XLM"])
	assert.Nil(t, got["USDC:"+testIssuer])
}

func TestPrices_PreservesPartialOnContextCancel(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.delay = 100 * time.Millisecond
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Price7d: [][2]float64{{1, 0.15}, {2, 0.16}}})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	svc := NewPricesService(expert, nil, PricesServiceConfig{MaxConcurrent: 1}, nil)
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

	completeMissingResults(tokens, result, staleFallback)
	require.Len(t, result, len(tokens))
	assert.Nil(t, result["XLM"])
	require.NotNil(t, result["USDC:"+testIssuer])
	assert.Equal(t, "1", result["USDC:"+testIssuer].CurrentPrice)
	assert.Nil(t, result["BOGUS:"+testIssuer])
}

func TestCompute24hChange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		current float64
		candles [][2]float64
		want    *string
	}{
		{"no candles", 1.0, nil, nil},
		{"single candle", 1.0, [][2]float64{{1, 1}}, nil},
		{"zero prior denom", 1.0, [][2]float64{{1, 0}, {2, 0.5}}, nil},
		{"+10% rise vs prior", 1.10, [][2]float64{{1, 1.0}, {2, 1.05}}, ptrStr("10")},
		{"-25% drop vs prior", 0.90, [][2]float64{{1, 1.2}, {2, 1.0}}, ptrStr("-25")},
		{"8 candles uses index len-2", 0.16, [][2]float64{{1, 0.18}, {2, 0.17}, {3, 0.169}, {4, 0.17}, {5, 0.165}, {6, 0.161}, {7, 0.158}, {8, 0.159}}, ptrStr("1.27")},
		{"rounding to 2 decimals", 1.0001, [][2]float64{{1, 1.0}, {2, 0.999}}, ptrStr("0.01")},
		{"negative zero collapses to 0", 0.999999, [][2]float64{{1, 1.0}, {2, 1.0}}, ptrStr("0")},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := compute24hChange(tc.current, tc.candles)
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tc.want, *got)
		})
	}
}

func TestFormatPrice(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0.15968791272173", formatPrice(0.15968791272173))
	assert.Equal(t, "1", formatPrice(1.0))
	assert.Equal(t, "0.0000001", formatPrice(0.0000001)) // no scientific notation
	assert.Equal(t, "0", formatPrice(0))
}

func ptrStr(s string) *string { return &s }
