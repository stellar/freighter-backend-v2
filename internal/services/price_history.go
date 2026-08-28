package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils/assetid"
)

const (
	priceHistoryServiceName = "price-history"

	// historyCacheKeyPrefix carries the cached-entry SCHEMA version. It
	// rotated v1→v2 when cachedSeries gained the fetch-time `to` the
	// coverage guard is evaluated against: a v1 entry has no `to`, so a
	// mixed-fleet read of one would measure coverage against the zero
	// timestamp. A cold cache on deploy is what the segment is for.
	historyCacheKeyPrefix    = "pricehistory:v2"
	tokenStatsCacheKeyPrefix = "tokenstats:v1"

	historyCurrency = "USD"

	// emptySeriesCacheTTL is the flat negative-cache TTL for empty series
	// (§6.2). Unpriced contract tokens are the common SEP-41 case; without
	// this, every open of one would reach upstream uncacheably.
	emptySeriesCacheTTL = 15 * time.Minute

	defaultTokenStatsCacheTTL = time.Hour

	// allRangeFromFloor is 2015-09-01 — the `from` floor for the ALL range,
	// used directly for assets reporting created=0 (XLM) and as the fallback
	// when the advisory asset call fails. The API only returns buckets that
	// exist, so the floor returns the identical series.
	allRangeFromFloor = int64(1441065600)

	// coverageGuardRatio is §4.4 rule 5: the delta is withheld when the
	// series covers less than ~90% of the requested window. 1D keeps the
	// prices path's 23-25h band verbatim; ALL is exempt.
	coverageGuardRatio = 0.9

	allRange = "ALL"
	oneDay   = "1D"

	// metaWaitCap is the ceiling on the ALL-range `from` refinement's wait
	// for the advisory asset call (see metaWaitBudget).
	metaWaitCap = 3 * time.Second
)

// rangeSpec is one row of the range→request map (Appendix A.1). Every
// resolution is itself a valid upstream enum member, so no range relies on
// the silent-coarsening behavior — but the response resolution is still
// always derived from the returned timestamps, never assumed.
type rangeSpec struct {
	resolutionSec   int64
	window          time.Duration // 0 → ALL: from = max(asset.created, floor)
	defaultCacheTTL time.Duration
}

var priceHistoryRanges = map[string]rangeSpec{
	"1H":     {resolutionSec: 300, window: time.Hour, defaultCacheTTL: 5 * time.Minute},
	oneDay:   {resolutionSec: 900, window: 24 * time.Hour, defaultCacheTTL: 15 * time.Minute},
	"1W":     {resolutionSec: 3600, window: 7 * 24 * time.Hour, defaultCacheTTL: time.Hour},
	"1M":     {resolutionSec: 14400, window: 30 * 24 * time.Hour, defaultCacheTTL: 6 * time.Hour},
	"1Y":     {resolutionSec: 259200, window: 365 * 24 * time.Hour, defaultCacheTTL: 24 * time.Hour},
	allRange: {resolutionSec: 1209600, window: 0, defaultCacheTTL: 7 * 24 * time.Hour},
}

// IsValidPriceHistoryRange reports whether r is a member of the closed
// 1H|1D|1W|1M|1Y|ALL enum. Handlers 400 anything else before the service
// (or a Prometheus label) sees it.
func IsValidPriceHistoryRange(r string) bool {
	_, ok := priceHistoryRanges[r]
	return ok
}

// PriceHistoryServiceConfig tunes the history/stats orchestrator. Zero values
// fall back to safe defaults so callers can construct a service with
// PriceHistoryServiceConfig{}.
type PriceHistoryServiceConfig struct {
	// CacheTTLs overrides the per-range series cache TTLs (§6.2 defaults),
	// keyed by range enum member. Zero/absent entries keep the default.
	CacheTTLs map[string]time.Duration
	// FetchTimeout bounds each upstream fetch (shared singleflight budget).
	FetchTimeout time.Duration
	// MinVolume7dUSD is the §4.4 rule 2 warning threshold in USD. 0 disables
	// the guard.
	MinVolume7dUSD float64
	// Volume7dConversionDivisor converts the raw upstream volume7d into USD
	// (raw ÷ divisor). The units are UNCONFIRMED (§13), so the default 0
	// means "conversion not enabled" and the verdict evaluates false —
	// enabling the guard is a config change, never a code change. A failed
	// asset lookup is still reported as null, never false, regardless.
	Volume7dConversionDivisor float64
	// TokenStatsCacheTTL is the TTL of the tokenstats:v1 asset-payload cache
	// entry shared by the volume verdict, the ALL-range from, and the
	// token-stats endpoint.
	TokenStatsCacheTTL time.Duration
}

type priceHistoryService struct {
	stellarExpert types.StellarExpertService
	redis         JSONCache
	prices        types.PricesService
	cfg           PriceHistoryServiceConfig
	svcMetrics    *metrics.Service
	pricesMetrics *metrics.Prices
	// fetchGroup coalesces concurrent upstream fetches per cache key
	// (series and asset-payload keys share the group; keys never collide).
	fetchGroup singleflight.Group
}

// NewPriceHistoryService wires the history/stats orchestrator. redis may be
// nil (every request then hits upstream); prices supplies the 30s-cached spot
// the delta anchors on; pricesMetrics may be nil for tests.
func NewPriceHistoryService(stellarExpert types.StellarExpertService, redis JSONCache, prices types.PricesService, cfg PriceHistoryServiceConfig, metricsService *metrics.Service, pricesMetrics *metrics.Prices) PriceHistoryAndStatsService {
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = defaultMissFetchTTL
	}
	if cfg.TokenStatsCacheTTL <= 0 {
		cfg.TokenStatsCacheTTL = defaultTokenStatsCacheTTL
	}
	return &priceHistoryService{
		stellarExpert: stellarExpert,
		redis:         redis,
		prices:        prices,
		cfg:           cfg,
		svcMetrics:    metricsService,
		pricesMetrics: pricesMetrics,
	}
}

func (s *priceHistoryService) Name() string { return priceHistoryServiceName }

// GetPriceHistory returns the chart payload for one canonical token id. No
// data is (points: [], change: null) — never an error; errors are reserved
// for transient upstream/system failures the handler maps to 5xx.
func (s *priceHistoryService) GetPriceHistory(ctx context.Context, canonical, network, historyRange string) (_ *types.TokenPriceHistory, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(s.svcMetrics, priceHistoryServiceName, "GetPriceHistory", network, time.Since(start).Seconds(), err)
	}()

	if network != types.PUBLIC && network != types.TESTNET {
		return nil, fmt.Errorf("unsupported network for price history: %s", network)
	}
	spec, ok := priceHistoryRanges[historyRange]
	if !ok {
		return nil, fmt.Errorf("unsupported price history range: %s", historyRange)
	}
	cacheNet := strings.ToLower(network)

	// The series and the asset payload resolve concurrently; each has its
	// own cache and singleflight key. The asset call is advisory (volume
	// verdict; ALL-range `from`): its failure yields lowVolume: null while
	// the series still returns. Deliberately NOT the prices service's
	// cancel-candles-on-asset-not-found behavior — here candles are the
	// payload (B.3).
	var (
		got       series
		seriesErr error
		meta      *types.StellarExpertAsset
		metaErr   error
		spot      string
		spotOK    bool
		wg        sync.WaitGroup
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		got, seriesErr = s.getSeries(ctx, network, cacheNet, canonical, historyRange, spec)
	}()
	go func() {
		defer wg.Done()
		meta, metaErr = s.getAssetMeta(ctx, network, cacheNet, canonical)
	}()
	// Spot joins the fan-out rather than running after it. The delta's two
	// inputs are independent — the series comes from /candles, the spot from
	// the prices service — and each carries its own multi-second fetch
	// budget, so serially they stack to roughly twice the http.Server
	// WriteTimeout and the connection dies before any status line is
	// written. Fetching it unconditionally costs one extra prices lookup on
	// series-less tokens, which the prices service's 30s positive and
	// negative caches absorb.
	go func() {
		defer wg.Done()
		spot, spotOK = s.spotPrice(ctx, canonical, network)
	}()
	wg.Wait()
	if seriesErr != nil {
		return nil, seriesErr
	}
	points := got.points
	if points == nil {
		points = make([]types.PricePoint, 0)
	}

	return &types.TokenPriceHistory{
		Range:             historyRange,
		Currency:          historyCurrency,
		ResolutionSeconds: deriveResolutionSeconds(points, spec.resolutionSec),
		LowVolume:         s.volumeVerdict(meta, metaErr, network),
		Change:            computeChange(historyRange, spec, points, got.to, spot, spotOK),
		Points:            points,
	}, nil
}

// cachedSeries is the on-disk shape of one pricehistory:v2 entry. Redis
// expiry alone governs freshness. Empty series are cached too (negative
// caching, emptySeriesCacheTTL).
type cachedSeries struct {
	Points []types.PricePoint `json:"points"`
	// To is the unix time the upstream window ended at when this series was
	// fetched. The §4.4 rule 5 coverage guard measures the series against
	// the window that was REQUESTED, so it must be evaluated against this
	// value — not wall-clock now, which would additionally charge the series
	// for however long it has since sat in Redis.
	To int64 `json:"to,omitempty"`
}

// series is one resolved history series plus the window end it was fetched
// against, carried together because the coverage guard needs both.
type series struct {
	points []types.PricePoint
	to     time.Time
}

func (s *priceHistoryService) getSeries(ctx context.Context, network, cacheNet, canonical, historyRange string, spec rangeSpec) (series, error) {
	key := historyCacheKey(cacheNet, canonical, historyRange)
	if cached, ok := s.loadCachedSeries(ctx, key); ok {
		s.recordHistoryCacheOutcome(network, historyRange, "hit")
		return cached, nil
	}
	s.recordHistoryCacheOutcome(network, historyRange, "miss")

	// Coalesce concurrent misses; the shared fetch runs under its own budget
	// so one caller's cancellation can't poison other in-flight waiters.
	ch := s.fetchGroup.DoChan(key, func() (any, error) {
		fctx, cancel := context.WithTimeout(context.Background(), s.cfg.FetchTimeout)
		defer cancel()
		return s.fetchSeries(fctx, network, cacheNet, canonical, historyRange, spec, key)
	})
	select {
	case <-ctx.Done():
		return series{}, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return series{}, res.Err
		}
		got, _ := res.Val.(series)
		return got, nil
	}
}

func (s *priceHistoryService) loadCachedSeries(ctx context.Context, key string) (series, bool) {
	if s.redis == nil {
		return series{}, false
	}
	cached, err := s.redis.MGetJSON(ctx, []string{key}, func() any { return new(cachedSeries) })
	if err != nil {
		logger.Warn("price-history: redis MGet failed; bypassing cache", "error", err)
		if s.pricesMetrics != nil {
			s.pricesMetrics.RedisErrors.WithLabelValues("mget").Inc()
		}
		return series{}, false
	}
	entry, _ := cached[key].(*cachedSeries)
	if entry == nil {
		return series{}, false
	}
	out := series{points: entry.Points}
	if out.points == nil {
		out.points = make([]types.PricePoint, 0)
	}
	if entry.To > 0 {
		out.to = time.Unix(entry.To, 0).UTC()
	} else {
		// Defensive: an entry written without a fetch time (only reachable
		// if the schema segment above is ever reused). Treat it as fetched
		// now, which is the pre-fix behavior.
		out.to = time.Now().UTC()
	}
	return out, true
}

// fetchSeries performs one upstream candles fetch and writes the result —
// including an empty one — to Redis. ErrAssetNotFound/ErrAssetMalformed map
// to an empty series (an unknown asset and an untraded asset are
// indistinguishable to the UI); transient failures return errors and are
// never cached.
func (s *priceHistoryService) fetchSeries(ctx context.Context, network, cacheNet, canonical, historyRange string, spec rangeSpec, key string) (series, error) {
	// `to` is NOW, deliberately untruncated. Rounding it back to the last
	// completed bucket would drop up to a full bucket off the right edge of
	// every chart — 15 minutes on 1D, but 3 days on 1Y and 2 weeks on ALL,
	// and the ALL entry then carries that staleness for its 7-day TTL. The
	// final bucket of a live series is always in progress; its close is the
	// newest trade and is exactly the point §4.2 anchors the delta's other
	// end against. Truncation bought nothing in return: the cache key
	// contains no timestamp, so aligned windows never shared an entry.
	to := time.Now().UTC()

	var from time.Time
	if spec.window > 0 {
		from = to.Add(-spec.window)
	} else {
		// ALL: from = max(asset.created, the 2015-09-01 floor). The asset
		// payload is the same cached entry the stats endpoint reads; on
		// failure fall back to the floor, which returns the identical series
		// since the API only returns buckets that exist.
		//
		// The wait is sub-budgeted because this call and the candles call
		// share ONE fetch budget. The asset payload is advisory here — the
		// floor returns the same series — while the candles are the payload,
		// so the refinement must never be allowed to spend the budget the
		// chart needs.
		fromUnix := allRangeFromFloor
		mctx, cancelMeta := context.WithTimeout(ctx, metaWaitBudget(ctx))
		meta, err := s.getAssetMeta(mctx, network, cacheNet, canonical)
		cancelMeta()
		if err == nil && meta != nil && meta.Created > allRangeFromFloor {
			fromUnix = meta.Created
		}
		from = time.Unix(fromUnix, 0).UTC()
	}

	candles, err := s.stellarExpert.GetAssetCandles(ctx, network, assetid.ToStellarExpert(canonical), from, to, int(spec.resolutionSec))
	if err != nil {
		if errors.Is(err, ErrAssetNotFound) || errors.Is(err, ErrAssetMalformed) {
			empty := series{points: make([]types.PricePoint, 0), to: to}
			s.cacheSeries(ctx, key, empty, emptySeriesCacheTTL)
			return empty, nil
		}
		return series{}, err
	}

	points := make([]types.PricePoint, 0, len(candles))
	for _, c := range candles {
		points = append(points, types.PricePoint{T: c.TS(), P: formatPrice(c.Close())})
	}

	ttl := spec.defaultCacheTTL
	if override, ok := s.cfg.CacheTTLs[historyRange]; ok && override > 0 {
		ttl = override
	}
	if len(points) == 0 {
		ttl = emptySeriesCacheTTL
	}
	fetched := series{points: points, to: to}
	s.cacheSeries(ctx, key, fetched, ttl)
	return fetched, nil
}

// metaWaitBudget bounds how long the ALL-range `from` refinement may wait on
// the advisory asset call before falling back to the 1441065600 floor. It
// spends at most metaWaitCap, and never more than half of whatever remains
// of the shared fetch budget — so a hanging /asset endpoint costs the chart
// at most half its budget and GetAssetCandles always still gets issued. A
// non-positive result means the budget is already gone; the timeout then
// fires immediately and the floor is used, which is the correct answer.
func metaWaitBudget(ctx context.Context) time.Duration {
	budget := metaWaitCap
	if deadline, ok := ctx.Deadline(); ok {
		if half := time.Until(deadline) / 2; half < budget {
			budget = half
		}
	}
	if budget < 0 {
		budget = 0
	}
	return budget
}

func (s *priceHistoryService) cacheSeries(ctx context.Context, key string, value series, ttl time.Duration) {
	if s.redis == nil {
		return
	}
	if err := s.redis.SetJSON(ctx, key, cachedSeries{Points: value.points, To: value.to.Unix()}, ttl); err != nil {
		logger.Warn("price-history: redis SET failed", "key", key, "error", err)
		if s.pricesMetrics != nil {
			s.pricesMetrics.RedisErrors.WithLabelValues("set").Inc()
		}
	}
}

// cachedAssetMeta is the on-disk shape of one tokenstats:v1 entry — the
// asset-payload subset shared by the volume verdict, the ALL-range `from`,
// and the token-stats endpoint. Supply is kept as a string because real
// supplies exceed float64's exact-integer range (and an empty json.Number
// does not marshal).
type cachedAssetMeta struct {
	Price    float64 `json:"price,omitempty"`
	Supply   string  `json:"supply,omitempty"`
	Decimals *int    `json:"decimals,omitempty"`
	Volume7d float64 `json:"volume7d,omitempty"`
	Created  int64   `json:"created,omitempty"`
	Funded   *int64  `json:"funded,omitempty"`
}

func (m *cachedAssetMeta) toAsset() *types.StellarExpertAsset {
	asset := &types.StellarExpertAsset{
		Price:    m.Price,
		Decimals: m.Decimals,
		Volume7d: m.Volume7d,
		Created:  m.Created,
	}
	asset.Supply = json.Number(m.Supply)
	asset.Trustlines.Funded = m.Funded
	return asset
}

func assetToCachedMeta(a *types.StellarExpertAsset) cachedAssetMeta {
	return cachedAssetMeta{
		Price:    a.Price,
		Supply:   a.Supply.String(),
		Decimals: a.Decimals,
		Volume7d: a.Volume7d,
		Created:  a.Created,
		Funded:   a.Trustlines.Funded,
	}
}

// getAssetMeta returns the (1h-cached, singleflight-coalesced) asset payload
// for one canonical id. Only positive payloads are cached.
func (s *priceHistoryService) getAssetMeta(ctx context.Context, network, cacheNet, canonical string) (*types.StellarExpertAsset, error) {
	key := tokenStatsCacheKey(cacheNet, canonical)
	if s.redis != nil {
		cached, err := s.redis.MGetJSON(ctx, []string{key}, func() any { return new(cachedAssetMeta) })
		if err != nil {
			logger.Warn("price-history: redis MGet failed; bypassing asset cache", "error", err)
			if s.pricesMetrics != nil {
				s.pricesMetrics.RedisErrors.WithLabelValues("mget").Inc()
			}
		} else if entry, _ := cached[key].(*cachedAssetMeta); entry != nil {
			s.recordStatsCacheOutcome(network, "hit")
			return entry.toAsset(), nil
		}
	}
	s.recordStatsCacheOutcome(network, "miss")

	ch := s.fetchGroup.DoChan(key, func() (any, error) {
		fctx, cancel := context.WithTimeout(context.Background(), s.cfg.FetchTimeout)
		defer cancel()
		asset, err := s.stellarExpert.GetAsset(fctx, network, assetid.ToStellarExpert(canonical))
		if err != nil {
			return nil, err
		}
		if s.redis != nil {
			if err := s.redis.SetJSON(fctx, key, assetToCachedMeta(asset), s.cfg.TokenStatsCacheTTL); err != nil {
				logger.Warn("price-history: redis SET failed", "key", key, "error", err)
				if s.pricesMetrics != nil {
					s.pricesMetrics.RedisErrors.WithLabelValues("set").Inc()
				}
			}
		}
		return asset, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		asset, _ := res.Val.(*types.StellarExpertAsset)
		return asset, nil
	}
}

// volumeVerdict is the §4.4 rule 2 low-volume verdict. It is a pure function
// of the asset payload; when that payload is unavailable the verdict is null
// — NEVER false, since defaulting to false would drop the warning during
// exactly the upstream degradation where a painted price goes unchallenged —
// and the null is counted so the quadrant is operator-visible. While the
// volume7d unit conversion is unconfirmed (divisor 0, the shipped default)
// the verdict evaluates false.
func (s *priceHistoryService) volumeVerdict(meta *types.StellarExpertAsset, metaErr error, network string) *bool {
	if metaErr != nil || meta == nil {
		if s.pricesMetrics != nil {
			s.pricesMetrics.VolumeVerdictNull.WithLabelValues(network).Inc()
		}
		return nil
	}
	verdict := false
	if s.cfg.Volume7dConversionDivisor > 0 && s.cfg.MinVolume7dUSD > 0 {
		verdict = meta.Volume7d/s.cfg.Volume7dConversionDivisor < s.cfg.MinVolume7dUSD
	}
	return &verdict
}

// computeChange is the §4.2 spot-anchored delta: absolute = spot −
// first plotted close, percent = the same, relative, ×100 (rounded to two
// decimals exactly like percentagePriceChange24h — D8). It is computed per
// request from the cached series plus the 30s-cached spot and never cached
// as a value. Returns nil when the coverage guard rejects the window, when
// there is no spot, or when the anchor is unusable.
func computeChange(historyRange string, spec rangeSpec, points []types.PricePoint, fetchedTo time.Time, spotStr string, spotOK bool) *types.PriceChange {
	if len(points) == 0 {
		return nil
	}
	if !coverageOK(historyRange, spec, points, fetchedTo) {
		return nil
	}
	if !spotOK {
		return nil
	}
	spot, err := strconv.ParseFloat(spotStr, 64)
	if err != nil {
		return nil
	}
	startStr := points[0].P
	startPrice, err := strconv.ParseFloat(startStr, 64)
	if err != nil || startPrice == 0 {
		return nil
	}

	absolute, ok := subtractDecimalStrings(spotStr, startStr)
	if !ok {
		return nil
	}

	// Identical rounding to change24hFromCandles so the 1D header value
	// equals percentagePriceChange24h on identical inputs (D8).
	percent := (spot - startPrice) / startPrice * 100
	rounded := math.Round(percent*100) / 100
	if rounded == 0 {
		rounded = 0 // collapse negative zero for byte-stable JSON
	}
	return &types.PriceChange{
		Absolute: absolute,
		Percent:  strconv.FormatFloat(rounded, 'f', -1, 64),
	}
}

// coverageOK is the §4.4 rule 5 guard. It tests the age of the oldest point
// against the requested window — never the point count (sparse series with
// old-enough coverage still support a delta, A.6). 1D keeps the prices
// path's 23-25h band verbatim; longer ranges require ≥~90% coverage; ALL is
// exempt because partial coverage is its definition.
//
// `fetchedTo` is the window end the series was actually fetched against, not
// wall-clock now. The distinction is the whole point: series cache long (15m
// on 1D by default, and operator-tunable higher), so measuring against now
// charges a cached series for its own cache age and pushes 1D past the upper
// 25h bound for the tail of every cache window — nulling `change` on the
// detail header while the list row still shows it.
func coverageOK(historyRange string, spec rangeSpec, points []types.PricePoint, fetchedTo time.Time) bool {
	if historyRange == allRange {
		return true
	}
	to := fetchedTo
	if to.IsZero() {
		to = time.Now().UTC()
	}
	oldestAge := to.Sub(time.Unix(points[0].T, 0))
	if historyRange == oneDay {
		return oldestAge >= minCandleWindow && oldestAge <= maxCandleWindow
	}
	return float64(oldestAge) >= coverageGuardRatio*float64(spec.window)
}

// spotPrice reads the live spot through the prices service's 30s-cached
// path, so the delta moves with the header price rather than an
// independently-fetched value.
func (s *priceHistoryService) spotPrice(ctx context.Context, canonical, network string) (string, bool) {
	if s.prices == nil {
		return "", false
	}
	prices, err := s.prices.GetPrices(ctx, []string{canonical}, network)
	if err != nil {
		return "", false
	}
	entry := prices[canonical]
	if entry == nil || entry.CurrentPrice == "" {
		return "", false
	}
	return entry.CurrentPrice, true
}

// deriveResolutionSeconds implements the §6.1 rule: the smallest gap between
// adjacent timestamps when the series has ≥2 points, else the requested
// resolution. Never trust that the requested resolution was honored —
// upstream silently coarsens (A.1).
func deriveResolutionSeconds(points []types.PricePoint, requestedSec int64) int64 {
	if len(points) < 2 {
		return requestedSec
	}
	smallest := int64(0)
	for i := 1; i < len(points); i++ {
		gap := points[i].T - points[i-1].T
		if gap <= 0 {
			continue
		}
		if smallest == 0 || gap < smallest {
			smallest = gap
		}
	}
	if smallest == 0 {
		return requestedSec
	}
	return smallest
}

// subtractDecimalStrings computes a−b exactly for two plain decimal strings
// (the formatPrice output shape: fixed-point, no exponent) so the absolute
// delta carries no float64 subtraction artifacts. The result keeps at most
// max(dec(a), dec(b)) decimals — exact for decimal subtraction — with
// trailing zeros trimmed.
func subtractDecimalStrings(a, b string) (string, bool) {
	ra, okA := new(big.Rat).SetString(a)
	rb, okB := new(big.Rat).SetString(b)
	if !okA || !okB {
		return "", false
	}
	prec := decimalPlaces(a)
	if p := decimalPlaces(b); p > prec {
		prec = p
	}
	out := new(big.Rat).Sub(ra, rb).FloatString(prec)
	if strings.Contains(out, ".") {
		out = strings.TrimRight(out, "0")
		out = strings.TrimSuffix(out, ".")
	}
	if out == "-0" || out == "" {
		out = "0"
	}
	return out, true
}

func decimalPlaces(s string) int {
	if idx := strings.IndexByte(s, '.'); idx >= 0 {
		return len(s) - idx - 1
	}
	return 0
}

func (s *priceHistoryService) recordHistoryCacheOutcome(network, historyRange, outcome string) {
	if s.pricesMetrics == nil {
		return
	}
	s.pricesMetrics.HistoryCacheOutcomes.WithLabelValues(network, historyRange, outcome).Inc()
}

func (s *priceHistoryService) recordStatsCacheOutcome(network, outcome string) {
	if s.pricesMetrics == nil {
		return
	}
	s.pricesMetrics.TokenStatsCacheOutcomes.WithLabelValues(network, outcome).Inc()
}

func historyCacheKey(cacheNet, canonical, historyRange string) string {
	return historyCacheKeyPrefix + ":" + cacheNet + ":" + canonical + ":" + historyRange
}

func tokenStatsCacheKey(cacheNet, canonical string) string {
	return tokenStatsCacheKeyPrefix + ":" + cacheNet + ":" + canonical
}
