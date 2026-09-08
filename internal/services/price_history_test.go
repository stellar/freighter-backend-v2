package services

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const (
	// SolvBTC — the one priced SEP-41 contract token measured in A.7.
	solvBTCContract = "CBIJBDNZNF4X35BJ4FFZWCDBSCKOP5NB4PLG4SNENRMLAPYG4P5FM6VN"
	// A Soroswap LP token — asset payload exists, candles are empty (A.7).
	unpricedContract = "CBUIULAWELK3QWVO55TUFKTWE7BHC2BOESOIIV3FJKG47GAZWDUMOZFC"

	// Raw-shaped volume7d fixtures (A.2 measures ~1e14 for XLM). Fixtures
	// must NEVER be USD-scaled: a USD-authored fixture passes under either
	// unit convention and hides a broken conversion.
	xlmRawVolume7d = 103319258398384 // captured real XLM payload: ÷1e7 ≈ $10.3M/wk
	lowRawVolume7d = 18850000000     // KALE-shaped: ÷1e7 ≈ $1,885/wk — below $7k

	stroopDivisor = 1e7
)

// historyCandles builds candles whose first entry is `oldestAge` before
// `now` (truncated to the hour) and whose subsequent entries step forward
// stepSec. closes fill index 4; opens (index 1) are deliberately different
// so nothing can silently anchor on them.
func historyCandles(now time.Time, oldestAge time.Duration, stepSec int64, closes ...float64) []types.StellarExpertCandle {
	base := now.Truncate(time.Hour).Add(-oldestAge).Unix()
	out := make([]types.StellarExpertCandle, len(closes))
	for i, cl := range closes {
		ts := float64(base + int64(i)*stepSec)
		out[i] = types.StellarExpertCandle{ts, cl * 1.5, 0, 0, cl, 0, 0, 0}
	}
	return out
}

func spotPrices(spot string) *utils.MockPricesService {
	return &utils.MockPricesService{GetPricesFunc: func(ctx context.Context, tokens []string, network string) (map[string]*types.PriceEntry, error) {
		out := make(map[string]*types.PriceEntry, len(tokens))
		for _, tok := range tokens {
			out[tok] = &types.PriceEntry{CurrentPrice: spot}
		}
		return out, nil
	}}
}

func newHistoryService(expert types.StellarExpertService, cache JSONCache, prices types.PricesService, cfg PriceHistoryServiceConfig, pm *metrics.Prices) types.PriceHistoryService {
	return NewPriceHistoryService(expert, cache, prices, cfg, nil, pm)
}

func TestPriceHistory_HappyPath1D(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.160259, Volume7d: xlmRawVolume7d})
	expert.SetCandles("XLM", historyCandles(now, 24*time.Hour, 900, 0.1589, 0.1592, 0.1601))

	svc := newHistoryService(expert, nil, spotPrices("0.160259"), PriceHistoryServiceConfig{}, nil)
	got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.NoError(t, err)

	assert.Equal(t, "1D", got.Range)
	assert.Equal(t, "USD", got.Currency)
	assert.Equal(t, int64(900), got.ResolutionSeconds, "derived from adjacent timestamps")
	require.Len(t, got.Points, 3)
	assert.Equal(t, "0.1589", got.Points[0].P, "points carry closes")
	assert.Equal(t, int64(900), got.Points[1].T-got.Points[0].T)

	require.NotNil(t, got.Change)
	// §6.1 worked example: spot 0.160259 − first close 0.1589 = 0.001359 ≈ 0.86%.
	assert.Equal(t, "0.001359", got.Change.Absolute)
	assert.Equal(t, "0.86", got.Change.Percent)

	// Verdict input present but the conversion is not enabled, so the check
	// could not run: null, not a false "we checked and it's fine".
	assert.Nil(t, got.LowVolume)

	// The requested resolution is itself a valid upstream enum member.
	assert.Equal(t, 900, expert.LastCandleResolution("XLM"))
}

// Fact 2 (§5): upstream silently coarsens oversized windows. The service must
// derive resolutionSeconds from the returned timestamps, never echo the
// request.
func TestPriceHistory_ResolutionDerivedWhenCoarsened(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
	// 1Y requests 259200 (3d), but the fake returns 2w buckets.
	expert.SetCandles("XLM", historyCandles(now, 360*24*time.Hour, 1209600, 0.10, 0.11, 0.12))

	svc := newHistoryService(expert, nil, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
	got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1Y")
	require.NoError(t, err)

	assert.Equal(t, 259200, expert.LastCandleResolution("XLM"), "1Y requests the valid 3d enum member")
	assert.Equal(t, int64(1209600), got.ResolutionSeconds, "resolution must be derived from data")
}

// Fewer than 2 points cannot yield a gap; the requested resolution is used.
func TestPriceHistory_ResolutionFallsBackToRequestedOnSinglePoint(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
	expert.SetCandles("XLM", historyCandles(now, 2*time.Hour, 900, 0.159))

	svc := newHistoryService(expert, nil, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
	got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.NoError(t, err)

	assert.Equal(t, int64(900), got.ResolutionSeconds)
	require.Len(t, got.Points, 1)
	assert.Nil(t, got.Change, "2h coverage fails the 1D 23-25h guard")
}

// A priced contract token charts like any classic asset, and the upstream id
// is the bare contract id, verbatim (A.7).
func TestPriceHistory_PricedContractToken(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set(solvBTCContract, &types.StellarExpertAsset{Price: 66683, Volume7d: xlmRawVolume7d})
	expert.SetCandles(solvBTCContract, historyCandles(now, 24*time.Hour, 900, 66000, 66500))

	svc := newHistoryService(expert, nil, spotPrices("66683"), PriceHistoryServiceConfig{}, nil)
	got, err := svc.GetPriceHistory(context.Background(), solvBTCContract, types.PUBLIC, "1D")
	require.NoError(t, err)

	require.Len(t, got.Points, 2)
	assert.Equal(t, "66000", got.Points[0].P)
	assert.Equal(t, 1, expert.CandleCallCount(solvBTCContract), "wire id is the bare contract id")
	require.NotNil(t, got.Change)
}

// No data is a 200-shaped result — points: [], change: null — never an
// error, and the empty series is negatively cached at the flat 15m TTL so
// unpriced contract tokens (the common SEP-41 case) don't hit upstream per
// open.
func TestPriceHistory_EmptySeriesNegativelyCached(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.Set(unpricedContract, &types.StellarExpertAsset{Price: 0}) // payload exists, no candles configured → empty
	cache := newFakeJSONCache()

	svc := newHistoryService(expert, cache, spotPrices("1"), PriceHistoryServiceConfig{}, nil)
	got, err := svc.GetPriceHistory(context.Background(), unpricedContract, types.PUBLIC, "1D")
	require.NoError(t, err)
	require.NotNil(t, got.Points)
	assert.Empty(t, got.Points)
	assert.Nil(t, got.Change)
	assert.Equal(t, 1, expert.CandleCallCount(unpricedContract))

	assert.Equal(t, 15*time.Minute, cache.TTL("pricehistory:v2:public:"+unpricedContract+":1D"),
		"empty series cache at the flat negative TTL")

	got, err = svc.GetPriceHistory(context.Background(), unpricedContract, types.PUBLIC, "1D")
	require.NoError(t, err)
	assert.Empty(t, got.Points)
	assert.Equal(t, 1, expert.CandleCallCount(unpricedContract),
		"second request within the negative TTL must make zero upstream candle calls")
}

// Upstream ErrAssetNotFound on candles is an empty series, not an error — an
// unknown asset and an untraded asset are indistinguishable to the UI.
func TestPriceHistory_AssetNotFoundIsEmptySeries(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.SetCandleErr("UNKNOWN-"+testIssuer+"-2", ErrAssetNotFound)

	svc := newHistoryService(expert, nil, spotPrices("1"), PriceHistoryServiceConfig{}, nil)
	got, err := svc.GetPriceHistory(context.Background(), "UNKNOWN:"+testIssuer, types.PUBLIC, "1W")
	require.NoError(t, err)
	require.NotNil(t, got.Points)
	assert.Empty(t, got.Points)
	assert.Nil(t, got.Change)
}

// A transient candles failure is an error (the handler maps it to 5xx) — it
// must never be confused with the empty-series success case, and must not be
// negatively cached.
func TestPriceHistory_TransientCandlesErrorIsError(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16})
	expert.SetCandleErr("XLM", errors.New("transport boom"))
	cache := newFakeJSONCache()

	svc := newHistoryService(expert, cache, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
	_, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.Error(t, err)
	assert.Equal(t, time.Duration(0), cache.TTL("pricehistory:v2:public:XLM:1D"), "transient failure must not be cached")
}

// Series cache TTLs are per range.
func TestPriceHistory_SeriesCachedAtPerRangeTTL(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
	expert.SetCandles("XLM", historyCandles(now, 6*24*time.Hour, 3600, 0.15, 0.16))
	cache := newFakeJSONCache()

	svc := newHistoryService(expert, cache, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
	_, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1W")
	require.NoError(t, err)
	assert.Equal(t, time.Hour, cache.TTL("pricehistory:v2:public:XLM:1W"), "1W default TTL is 1h")

	_, err = svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1W")
	require.NoError(t, err)
	assert.Equal(t, 1, expert.CandleCallCount("XLM"), "cache hit must not refetch")
}

// D8's one-formula property: percentagePriceChange24h from the prices path
// equals change.percent from range=1D when computed from identical series and
// spot inputs.
func TestPriceHistory_D8EqualityOnIdenticalInputs(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.160259, Volume7d: xlmRawVolume7d})
	expert.SetCandles("XLM", historyCandles(now, 24*time.Hour, 900, 0.1589, 0.1596, 0.1601))

	pricesSvc := NewPricesService(expert, nil, PricesServiceConfig{}, nil, nil)
	historySvc := newHistoryService(expert, nil, pricesSvc, PriceHistoryServiceConfig{}, nil)

	prices, err := pricesSvc.GetPrices(context.Background(), []string{"XLM"}, types.PUBLIC)
	require.NoError(t, err)
	require.NotNil(t, prices["XLM"])
	require.NotNil(t, prices["XLM"].PercentagePriceChange24h)

	hist, err := historySvc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.NoError(t, err)
	require.NotNil(t, hist.Change)

	assert.Equal(t, *prices["XLM"].PercentagePriceChange24h, hist.Change.Percent,
		"the 1D header delta and percentagePriceChange24h must be one formula")
}

// §4.4 rule 5: the delta is withheld when the series covers <90% of the
// requested window; the chart still draws. ALL is exempt.
func TestPriceHistory_CoverageGuard(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	t.Run("1Y with 4 months of data → null change, full series", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
		expert.SetCandles("XLM", historyCandles(now, 120*24*time.Hour, 259200, 0.10, 0.12, 0.14))

		svc := newHistoryService(expert, nil, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
		got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1Y")
		require.NoError(t, err)
		assert.Nil(t, got.Change)
		assert.Len(t, got.Points, 3, "the guard nulls the number, never the chart")
	})

	t.Run("1Y with ~360d of data → change present", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
		expert.SetCandles("XLM", historyCandles(now, 360*24*time.Hour, 259200, 0.10, 0.12, 0.14))

		svc := newHistoryService(expert, nil, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
		got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1Y")
		require.NoError(t, err)
		require.NotNil(t, got.Change)
	})

	t.Run("ALL is exempt from the guard", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Created: 1611161688, Volume7d: xlmRawVolume7d})
		expert.SetCandles("XLM", historyCandles(now, 60*24*time.Hour, 1209600, 0.10, 0.12))

		svc := newHistoryService(expert, nil, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
		got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "ALL")
		require.NoError(t, err)
		require.NotNil(t, got.Change, "partial coverage is ALL's definition")
	})

	t.Run("1D uses the 23-25h guard verbatim", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
		// 22h coverage is >90% of 24h but outside the [23h, 25h] band the
		// prices path enforces — the 1D guard survives verbatim so the two
		// surfaces can never disagree on when the number exists.
		expert.SetCandles("XLM", historyCandles(now, 22*time.Hour, 900, 0.15, 0.16))

		svc := newHistoryService(expert, nil, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
		got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
		require.NoError(t, err)
		assert.Nil(t, got.Change)
	})
}

// Spot is the 30s-cached prices path's value; when it is unavailable the
// delta is null (spot-anchored means no spot, no delta) but the series still
// serves.
func TestPriceHistory_NoSpotNullChange(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
	expert.SetCandles("XLM", historyCandles(now, 24*time.Hour, 900, 0.15, 0.16))

	svc := newHistoryService(expert, nil, &utils.MockPricesService{}, PriceHistoryServiceConfig{}, nil)
	got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.NoError(t, err)
	assert.Nil(t, got.Change)
	assert.Len(t, got.Points, 2)
}

func TestPriceHistory_VolumeVerdict(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	enabled := PriceHistoryServiceConfig{MinVolume7dUSD: 7000, Volume7dConversionDivisor: stroopDivisor}

	t.Run("captured real XLM payload reads false with conversion enabled", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
		expert.SetCandles("XLM", historyCandles(now, 24*time.Hour, 900, 0.15, 0.16))

		svc := newHistoryService(expert, nil, spotPrices("0.16"), enabled, nil)
		got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
		require.NoError(t, err)
		require.NotNil(t, got.LowVolume, "unit-sanity check: the reference asset must clear the threshold")
		assert.False(t, *got.LowVolume)
	})

	t.Run("below-threshold token warns and touches nothing else", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("THIN-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 0.01, Volume7d: lowRawVolume7d})
		expert.SetCandles("THIN-"+testIssuer+"-1", historyCandles(now, 24*time.Hour, 900, 0.009, 0.01))

		svc := newHistoryService(expert, nil, spotPrices("0.01"), enabled, nil)
		got, err := svc.GetPriceHistory(context.Background(), "THIN:"+testIssuer, types.PUBLIC, "1D")
		require.NoError(t, err)
		require.NotNil(t, got.LowVolume)
		assert.True(t, *got.LowVolume)
		assert.Len(t, got.Points, 2, "full series still returns")
		require.NotNil(t, got.Change, "delta still returns — the banner is the entire intervention")
	})

	// A token that WOULD be flagged once units are confirmed must not be
	// reported as false in the meantime: with no conversion there is no
	// check, and false would assert a passing verdict this token fails.
	t.Run("verdict is null while the conversion is unconfirmed", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("THIN-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 0.01, Volume7d: lowRawVolume7d})
		expert.SetCandles("THIN-"+testIssuer+"-1", historyCandles(now, 24*time.Hour, 900, 0.009, 0.01))

		// Default config: divisor 0 → no conversion → the check cannot run.
		svc := newHistoryService(expert, nil, spotPrices("0.01"), PriceHistoryServiceConfig{MinVolume7dUSD: 7000}, nil)
		got, err := svc.GetPriceHistory(context.Background(), "THIN:"+testIssuer, types.PUBLIC, "1D")
		require.NoError(t, err)
		assert.Nil(t, got.LowVolume, "the same payload reads true once the divisor is set")
	})

	// The config-state null is NOT the upstream-degradation null, so it must
	// not move the counter that means "the asset call failed".
	t.Run("the unconfirmed-conversion null does not count as upstream degradation", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
		expert.SetCandles("XLM", historyCandles(now, 24*time.Hour, 900, 0.15, 0.16))

		reg := prometheus.NewRegistry()
		pm := metrics.NewPrices(reg)
		svc := newHistoryService(expert, nil, spotPrices("0.16"), PriceHistoryServiceConfig{MinVolume7dUSD: 7000}, pm)
		got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
		require.NoError(t, err)
		require.Nil(t, got.LowVolume)
		assert.Equal(t, float64(0), testutil.ToFloat64(pm.VolumeVerdictNull.WithLabelValues(types.PUBLIC)))
	})

	// Distinct from the unconfirmed-conversion case above: the conversion
	// works, so the check runs — the operator has just chosen to flag
	// nothing. That is a real passing verdict, not an unknown.
	t.Run("threshold 0 disables the guard and stays false", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("THIN-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 0.01, Volume7d: lowRawVolume7d})
		expert.SetCandles("THIN-"+testIssuer+"-1", historyCandles(now, 24*time.Hour, 900, 0.009, 0.01))

		svc := newHistoryService(expert, nil, spotPrices("0.01"), PriceHistoryServiceConfig{MinVolume7dUSD: 0, Volume7dConversionDivisor: stroopDivisor}, nil)
		got, err := svc.GetPriceHistory(context.Background(), "THIN:"+testIssuer, types.PUBLIC, "1D")
		require.NoError(t, err)
		require.NotNil(t, got.LowVolume)
		assert.False(t, *got.LowVolume)
	})

	t.Run("asset fetch failure with candles success is null, never false", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.SetErr("XLM", errors.New("asset endpoint boom"))
		expert.SetCandles("XLM", historyCandles(now, 24*time.Hour, 900, 0.15, 0.16))

		reg := prometheus.NewRegistry()
		pm := metrics.NewPrices(reg)
		svc := newHistoryService(expert, nil, spotPrices("0.16"), enabled, pm)
		got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
		require.NoError(t, err, "the series is the payload; the asset call is advisory (B.3)")
		assert.Len(t, got.Points, 2, "an asset-side failure must not cancel or blank the chart")
		assert.Nil(t, got.LowVolume, "a failed lookup is never reported as false")
		assert.Equal(t, float64(1), testutil.ToFloat64(pm.VolumeVerdictNull.WithLabelValues(types.PUBLIC)),
			"every null verdict increments the operator-visible metric")
	})
}

// freighter_price_history_volume_verdict_null_total means "upstream
// degraded": the candles succeeded but the asset payload we grade volume on
// did not. A client that closes its connection mid-request also aborts the
// meta wait, with a context error — but nothing upstream went wrong, and
// counting it makes the metric track client behaviour instead of upstream
// health. lowVolume is still null either way; only the metric changes.
func TestPriceHistory_VolumeVerdictNullNotCountedForClientCancellation(t *testing.T) {
	t.Parallel()

	fetchedAt := time.Now().UTC()
	cache := newFakeJSONCache()
	require.NoError(t, cache.SetJSON(context.Background(), "pricehistory:v2:public:XLM:1D", cachedSeries{
		Points: []types.PricePoint{
			{T: fetchedAt.Add(-24 * time.Hour).Unix(), P: "0.15"},
			{T: fetchedAt.Add(-time.Hour).Unix(), P: "0.16"},
		},
		To: fetchedAt.Unix(),
	}, time.Hour))

	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
	expert.assetDelay = time.Minute // the meta wait cannot finish before the client leaves

	reg := prometheus.NewRegistry()
	pm := metrics.NewPrices(reg)
	svc := newHistoryService(expert, cache, spotPrices("0.16"), PriceHistoryServiceConfig{}, pm)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client is already gone

	got, err := svc.GetPriceHistory(ctx, "XLM", types.PUBLIC, "1D")
	require.NoError(t, err, "the series was cached, so the request still completes")
	assert.Nil(t, got.LowVolume, "the verdict is still null — it is genuinely unknown")
	assert.Equal(t, float64(0), testutil.ToFloat64(pm.VolumeVerdictNull.WithLabelValues(types.PUBLIC)),
		"a client leaving is not upstream degradation")
}

// ALL's `from` is max(asset.created, the 2015-09-01 floor); an asset-call
// failure falls back to the floor rather than failing the chart.
func TestPriceHistory_ALLRangeFrom(t *testing.T) {
	t.Parallel()

	t.Run("uses asset.created when later than the floor", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("USDC-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 1.0, Created: 1611161688, Volume7d: xlmRawVolume7d})
		expert.SetCandles("USDC-"+testIssuer+"-1", historyCandles(time.Now().UTC(), 60*24*time.Hour, 1209600, 0.99, 1.0))

		svc := newHistoryService(expert, nil, spotPrices("1"), PriceHistoryServiceConfig{}, nil)
		_, err := svc.GetPriceHistory(context.Background(), "USDC:"+testIssuer, types.PUBLIC, "ALL")
		require.NoError(t, err)
		assert.Equal(t, int64(1611161688), expert.LastCandleFrom("USDC-"+testIssuer+"-1").Unix())
	})

	t.Run("XLM's created=0 clamps to the floor", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Created: 0, Volume7d: xlmRawVolume7d})
		expert.SetCandles("XLM", historyCandles(time.Now().UTC(), 60*24*time.Hour, 1209600, 0.15, 0.16))

		svc := newHistoryService(expert, nil, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
		_, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "ALL")
		require.NoError(t, err)
		assert.Equal(t, int64(1441065600), expert.LastCandleFrom("XLM").Unix())
	})

	t.Run("asset-call failure falls back to the floor", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.SetErr("XLM", errors.New("asset endpoint boom"))
		expert.SetCandles("XLM", historyCandles(time.Now().UTC(), 60*24*time.Hour, 1209600, 0.15, 0.16))

		svc := newHistoryService(expert, nil, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
		got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "ALL")
		require.NoError(t, err)
		assert.Len(t, got.Points, 2)
		assert.Equal(t, int64(1441065600), expert.LastCandleFrom("XLM").Unix())
	})

	// §9 documents "asset payload fails, candles succeed → ALL falls back to
	// the 2015-09-01 floor". A HANGING /asset endpoint is that failure mode's
	// most likely shape, and it must not starve the candles call: the two
	// share one fetch budget, so an unbounded meta wait burns the whole
	// budget before GetAssetCandles is even issued and the fallback dies at
	// exactly the moment it should save the request.
	t.Run("a hanging asset call still leaves budget for candles", func(t *testing.T) {
		t.Parallel()
		expert := newFakeStellarExpert()
		expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Created: 1611161688, Volume7d: xlmRawVolume7d})
		expert.assetDelay = time.Minute // /asset hangs; candles are healthy
		// A non-zero candles delay makes the fake honour ctx, so a candles
		// call issued on an already-spent budget fails the way a real one
		// would rather than quietly succeeding.
		expert.delay = 50 * time.Millisecond
		expert.SetCandles("XLM", historyCandles(time.Now().UTC(), 60*24*time.Hour, 1209600, 0.15, 0.16))

		svc := newHistoryService(expert, nil, spotPrices("0.16"),
			PriceHistoryServiceConfig{FetchTimeout: 2 * time.Second}, nil)

		start := time.Now().UTC()
		got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, allRange)
		require.NoError(t, err)

		assert.Len(t, got.Points, 2, "candles must still be fetched")
		assert.Equal(t, int64(allRangeFromFloor), expert.LastCandleFrom("XLM").Unix(),
			"the meta wait times out into the documented floor fallback")
		// LastCandleTo is the wall clock at which the candles call was
		// issued: it must land around the half-budget mark, not after the
		// whole 2s budget has been spent waiting on /asset.
		assert.Less(t, expert.LastCandleTo("XLM").Sub(start), 1500*time.Millisecond,
			"the meta wait must consume at most half the shared budget, leaving the rest for candles")
	})
}

// The upstream window must end at `now`, not at the last completed bucket.
// Truncating `to` to the range's resolution drops up to a full bucket off the
// right edge of every chart — 15 minutes on 1D, but 3 days on 1Y and 2 weeks
// on ALL, and the ALL entry is then cached for 7 days on top of that. The
// cache key carries no timestamp, so the truncation bought nothing.
func TestPriceHistory_UpstreamWindowEndsAtNow(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	for _, tc := range []struct {
		historyRange string
		stepSec      int64
		oldestAge    time.Duration
	}{
		{"1D", 900, 24 * time.Hour},
		{"1Y", 259200, 360 * 24 * time.Hour},
		{allRange, 1209600, 360 * 24 * time.Hour},
	} {
		tc := tc
		t.Run(tc.historyRange, func(t *testing.T) {
			t.Parallel()
			expert := newFakeStellarExpert()
			expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
			expert.SetCandles("XLM", historyCandles(now, tc.oldestAge, tc.stepSec, 0.15, 0.16))

			svc := newHistoryService(expert, nil, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
			_, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, tc.historyRange)
			require.NoError(t, err)

			to := expert.LastCandleTo("XLM")
			require.False(t, to.IsZero())
			assert.WithinDuration(t, time.Now().UTC(), to, time.Minute,
				"`to` must be now, never truncated back to the last completed bucket")
		})
	}
}

// The final bucket of a live series is always in progress. Its timestamp sits
// past the last completed bucket boundary, and it must survive the round trip
// — it is the newest point the chart draws and the right edge of the line.
func TestPriceHistory_InProgressFinalBucketRoundTrips(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.17, Volume7d: xlmRawVolume7d})
	// Three 15m buckets ending with one that opened after the last completed
	// boundary — i.e. the bucket now sits inside.
	inProgress := now.Truncate(15 * time.Minute)
	candles := []types.StellarExpertCandle{
		{float64(inProgress.Add(-24 * time.Hour).Unix()), 0.20, 0, 0, 0.15, 0, 0, 0},
		{float64(inProgress.Add(-15 * time.Minute).Unix()), 0.20, 0, 0, 0.16, 0, 0, 0},
		{float64(inProgress.Unix()), 0.20, 0, 0, 0.169, 0, 0, 0},
	}
	expert.SetCandles("XLM", candles)

	svc := newHistoryService(expert, nil, spotPrices("0.17"), PriceHistoryServiceConfig{}, nil)
	got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.NoError(t, err)

	require.Len(t, got.Points, 3, "the in-progress bucket is a point like any other")
	assert.Equal(t, "0.169", got.Points[2].P)
	assert.Equal(t, inProgress.Unix(), got.Points[2].T)
	assert.Equal(t, int64(900), got.ResolutionSeconds)
	require.NotNil(t, got.Change, "the 23-25h guard still passes with an untruncated `to`")
}

// The series fetch and the spot fetch are independent, and each carries its
// own multi-second budget. Run serially they stack — worst case ~18s against
// a 10s http.Server WriteTimeout, so the connection dies before the handler
// can write anything at all. They must overlap.
func TestPriceHistory_SeriesAndSpotFetchConcurrently(t *testing.T) {
	t.Parallel()

	const probeWait = 3 * time.Second
	now := time.Now().UTC()

	seriesStarted := make(chan struct{})
	spotStarted := make(chan struct{})
	var overlapped atomic.Bool

	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
	expert.SetCandles("XLM", historyCandles(now, 24*time.Hour, 900, 0.15, 0.16))
	// The probe is deliberately one-sided: the candles call announces itself
	// and then waits, still in flight, to see whether the spot call starts.
	// Only that direction distinguishes overlap from "ran second and found
	// the first one's flag already set", which a symmetric probe cannot.
	expert.beforeCandles = func() {
		close(seriesStarted)
		select {
		case <-spotStarted:
			overlapped.Store(true)
		case <-time.After(probeWait):
		}
	}

	prices := &utils.MockPricesService{GetPricesFunc: func(ctx context.Context, tokens []string, network string) (map[string]*types.PriceEntry, error) {
		close(spotStarted)
		out := make(map[string]*types.PriceEntry, len(tokens))
		for _, tok := range tokens {
			out[tok] = &types.PriceEntry{CurrentPrice: "0.16"}
		}
		return out, nil
	}}

	svc := newHistoryService(expert, nil, prices, PriceHistoryServiceConfig{}, nil)
	got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.NoError(t, err)

	assert.True(t, overlapped.Load(),
		"the series fetch and the spot fetch must be in flight at the same time, not one after the other")
	require.NotNil(t, got.Change, "the delta still resolves from the concurrently-fetched spot")
	assert.Equal(t, "0.16", got.Points[1].P)
}

// The coverage guard measures the series against the window that was
// REQUESTED, so it has to be evaluated against the `to` that series was
// fetched with — not against wall-clock now. Recomputing to=now against a
// cached series ages it by however long it has sat in Redis, so a 1D series
// (15m TTL by default, and operator-tunable higher) drifts past the 23-25h
// band and nulls `change` for the tail of every cache window. That is a D8
// violation reachable purely through config.
func TestPriceHistory_CoverageEvaluatedAgainstFetchTimeNotCacheAge(t *testing.T) {
	t.Parallel()

	fetchedAt := time.Now().UTC().Add(-2 * time.Hour)
	cache := newFakeJSONCache()
	key := "pricehistory:v2:public:XLM:1D"
	require.NoError(t, cache.SetJSON(context.Background(), key, cachedSeries{
		Points: []types.PricePoint{
			{T: fetchedAt.Add(-24 * time.Hour).Unix(), P: "0.15"},
			{T: fetchedAt.Add(-12 * time.Hour).Unix(), P: "0.16"},
		},
		To: fetchedAt.Unix(),
	}, 4*time.Hour))

	expert := newFakeStellarExpert()
	svc := newHistoryService(expert, cache, spotPrices("0.17"), PriceHistoryServiceConfig{}, nil)

	got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.NoError(t, err)
	assert.Equal(t, 0, expert.CandleCallCount("XLM"), "served from cache")
	require.Len(t, got.Points, 2)
	require.NotNil(t, got.Change,
		"a cached 1D series is still a 24h series two hours later; cache age is not sparse data")
	assert.Equal(t, "0.02", got.Change.Absolute)
}

// The cached-series schema gained the fetch-time `to`, so the key-schema
// segment rotates with it: a v1 entry carries no `to` and would be evaluated
// against a zero timestamp.
func TestPriceHistory_KeySchemaRotatedForStoredFetchTime(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
	expert.SetCandles("XLM", historyCandles(now, 24*time.Hour, 900, 0.15, 0.16))
	cache := newFakeJSONCache()

	require.NoError(t, cache.SetJSON(context.Background(), "pricehistory:v1:public:XLM:1D",
		cachedSeries{Points: []types.PricePoint{{T: now.Unix(), P: "999"}}}, time.Hour))

	svc := newHistoryService(expert, cache, spotPrices("0.16"), PriceHistoryServiceConfig{}, nil)
	got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.NoError(t, err)

	assert.Equal(t, 1, expert.CandleCallCount("XLM"), "a v1 entry must not satisfy a v2 read")
	require.Len(t, got.Points, 2)
	assert.NotEqual(t, time.Duration(0), cache.TTL("pricehistory:v2:public:XLM:1D"))
}

func TestPriceHistory_CacheOutcomeMetrics(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Volume7d: xlmRawVolume7d})
	expert.SetCandles("XLM", historyCandles(now, 24*time.Hour, 900, 0.15, 0.16))
	cache := newFakeJSONCache()

	reg := prometheus.NewRegistry()
	pm := metrics.NewPrices(reg)
	svc := newHistoryService(expert, cache, spotPrices("0.16"), PriceHistoryServiceConfig{}, pm)

	_, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.NoError(t, err)
	_, err = svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.NoError(t, err)

	assert.Equal(t, float64(1), testutil.ToFloat64(pm.HistoryCacheOutcomes.WithLabelValues(types.PUBLIC, "1D", "miss")))
	assert.Equal(t, float64(1), testutil.ToFloat64(pm.HistoryCacheOutcomes.WithLabelValues(types.PUBLIC, "1D", "hit")))
}

// On the ALL range the asset payload is needed twice in one request — once
// for the lowVolume verdict, once for the `from` floor — but it is ONE fact
// about ONE asset. Resolving it twice double-counts every tokenstats cache
// outcome for ALL requests, so the metric that is supposed to show how well
// that cache is working reports a hit rate computed over phantom lookups.
func TestPriceHistory_ALLResolvesAssetMetaExactlyOnce(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set("XLM", &types.StellarExpertAsset{Price: 0.16, Created: 1611161688, Volume7d: xlmRawVolume7d})
	expert.SetCandles("XLM", historyCandles(now, 60*24*time.Hour, 1209600, 0.15, 0.16))

	reg := prometheus.NewRegistry()
	pm := metrics.NewPrices(reg)
	// The conversion is enabled so the verdict is a real value: a nil here
	// would prove nothing about whether the payload was resolved.
	cfg := PriceHistoryServiceConfig{MinVolume7dUSD: 7000, Volume7dConversionDivisor: stroopDivisor}
	svc := newHistoryService(expert, newFakeJSONCache(), spotPrices("0.16"), cfg, pm)

	got, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, allRange)
	require.NoError(t, err)
	require.NotNil(t, got.LowVolume, "the verdict still resolves off the shared payload")
	assert.Equal(t, int64(1611161688), expert.LastCandleFrom("XLM").Unix(),
		"the `from` floor still refines off the shared payload")

	outcomes := testutil.ToFloat64(pm.TokenStatsCacheOutcomes.WithLabelValues(types.PUBLIC, "miss")) +
		testutil.ToFloat64(pm.TokenStatsCacheOutcomes.WithLabelValues(types.PUBLIC, "hit"))
	assert.Equal(t, float64(1), outcomes,
		"one request needs the asset payload once, so it must record exactly one cache outcome")
	assert.Equal(t, 1, expert.CallCount("XLM"), "and issue at most one upstream asset call")
}

func TestPriceHistory_RejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	svc := newHistoryService(expert, nil, spotPrices("1"), PriceHistoryServiceConfig{}, nil)

	_, err := svc.GetPriceHistory(context.Background(), "XLM", types.FUTURENET, "1D")
	require.Error(t, err, "FUTURENET is not a prices network")

	_, err = svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "2D")
	require.Error(t, err, "range is a closed enum")
}
