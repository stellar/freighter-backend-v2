package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/freighter-backend-v2/internal/utils/assetid"
)

// The Dn / A.n / §n.n markers below refer to the token-price-graphs design doc.
// See the header comment in price_history.go for its path and what each marker
// form means.

const (
	pricesServiceName = "prices"

	defaultMaxConcurrent = 25
	defaultCacheTTL      = 30 * time.Second
	defaultMissFetchTTL  = 9 * time.Second

	// cacheKeyPrefix carries the cached-entry SCHEMA version, not the
	// service version. It rotated v1→v2 when cachedPriceEntry gained the
	// `unpriced` marker: a negative entry carries no `currentPrice`, so a
	// pod still running the old binary would decode one as a positive hit
	// and serve currentPrice: "". Rotating the segment keeps the two
	// schemas in disjoint keyspaces during a rollout; the cost is a cold
	// cache on deploy, which is exactly what the segment is for.
	cacheKeyPrefix = "prices:v2"

	// defaultNegativeCacheTTL is the default TTL for cached null entries —
	// tokens Stellar Expert reports as unpriceable (not found, malformed,
	// zero price). Negative caching deliberately breaks the positive-only
	// convention (§6.2): once clients stop filtering custom tokens, an
	// unpriced token in a balance list would otherwise hit upstream on
	// every 30-second poll cycle, uncacheably. Transient transport failures
	// and 5xx are NOT cached.
	//
	// It is deliberately its own knob rather than sharing the 15m the
	// history endpoint uses for empty series. The signals that land here
	// are not all authoritative: a degraded 200 with `price` omitted
	// decodes to 0, and a transient upstream 404/400 maps to
	// ErrAssetNotFound/ErrAssetMalformed. Those blips self-heal in ~30s,
	// but the cache entry is shared across every pod, so a long TTL turns a
	// momentary upstream wobble into a price blackout of that length. 120s
	// still dedupes four 30s poll cycles per blip while bounding the blast
	// radius to about two minutes.
	defaultNegativeCacheTTL = 2 * time.Minute

	// candlesWindow is the rolling window used to compute
	// percentagePriceChange24h from /asset/{id}/candles.
	candlesWindow = 24 * time.Hour

	// defaultCandlesResolutionSec is the bucket size that window is
	// requested at. 900 (15m) is REQUIRED by D8: it is the chart's 1D
	// resolution, and the whole point of D8 is that the list row, the
	// detail header, and the 1D chart compute one number from one series.
	//
	// It is nevertheless an operator knob, because 900 fetches ~97 records
	// per token where the previous 3600 fetched ~25 — a 4x increase in rows
	// pulled from a paid upstream on the hottest path in the service, with
	// no way to back it out without a deploy. Raising it back to 3600 is an
	// incident lever, not a supported configuration: it re-splits the two
	// formulas and the header will visibly disagree with the list row again.
	defaultCandlesResolutionSec = 900

	// minCandleWindow / maxCandleWindow bound how far before `to` the
	// oldest returned candle must open. With a truncated `to`, an asset
	// trading continuously yields candles[0] at exactly 24h ago; sparse
	// trading or upstream truncation can shift it later (closer to now) or
	// rarely earlier. ±1h around 24h covers normal bucket-boundary slack
	// while rejecting sparse-data drift that would make the result not
	// represent a 24h window. Kept verbatim through the D8 resolution
	// change: finer buckets only shrink the boundary slack (60 → 15
	// minutes), so the guard fires less, and any flips are null → value.
	minCandleWindow = 23 * time.Hour
	maxCandleWindow = 25 * time.Hour
)

// PricesServiceConfig tunes the orchestrator. Zero values fall back to safe
// defaults so callers can construct a service with PricesServiceConfig{}.
type PricesServiceConfig struct {
	CacheTTL         time.Duration
	MissFetchTimeout time.Duration
	MaxConcurrent    int
	// NegativeCacheTTL is the TTL for cached unpriceable ("unpriced")
	// entries. Zero falls back to defaultNegativeCacheTTL.
	NegativeCacheTTL time.Duration
	// CandlesResolutionSec is the bucket size the 24h-change candles window
	// is requested at. Zero falls back to defaultCandlesResolutionSec (900,
	// the D8-required chart alignment); see that constant for why raising it
	// is an incident lever rather than a tuning option.
	CandlesResolutionSec int
}

// JSONCache is the subset of *store.RedisStore the prices services depend
// on, factored as an interface so tests can substitute an in-memory cache.
// Implementations must tolerate concurrent use.
type JSONCache interface {
	MGetJSON(ctx context.Context, keys []string, makeDest func() any) (map[string]any, error)
	SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error
}

type pricesService struct {
	stellarExpert types.StellarExpertService
	redis         JSONCache
	cfg           PricesServiceConfig
	svcMetrics    *metrics.Service
	pricesMetrics *metrics.Prices
	// fetchGroup coalesces concurrent upstream fetches for the same cache key
	// so a thundering herd on a hot token (e.g. XLM at TTL expiry) issues one
	// Stellar Expert call instead of one per in-flight request.
	fetchGroup singleflight.Group
}

// NewPricesService wires the orchestrator. redis may be nil; if so, every
// request bypasses the cache and hits Stellar Expert. pricesMetrics may be
// nil for tests; counters become no-ops in that case.
func NewPricesService(stellarExpert types.StellarExpertService, redis JSONCache, cfg PricesServiceConfig, metricsService *metrics.Service, pricesMetrics *metrics.Prices) types.PricesService {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultMaxConcurrent
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaultCacheTTL
	}
	if cfg.MissFetchTimeout <= 0 {
		cfg.MissFetchTimeout = defaultMissFetchTTL
	}
	if cfg.NegativeCacheTTL <= 0 {
		cfg.NegativeCacheTTL = defaultNegativeCacheTTL
	}
	if cfg.CandlesResolutionSec <= 0 {
		cfg.CandlesResolutionSec = defaultCandlesResolutionSec
	}
	return &pricesService{stellarExpert: stellarExpert, redis: redis, cfg: cfg, svcMetrics: metricsService, pricesMetrics: pricesMetrics}
}

func (p *pricesService) Name() string { return pricesServiceName }

// cachedPriceEntry is the on-disk shape in Redis. Redis expiry alone governs
// freshness, so any entry that MGET returns is a live hit. Positive results
// cache at CacheTTL; authoritative nulls (Unpriced) cache at the flat
// negativePriceCacheTTL so unpriced tokens don't hit upstream per poll.
type cachedPriceEntry struct {
	CurrentPrice             string  `json:"currentPrice,omitempty"`
	PercentagePriceChange24h *string `json:"percentagePriceChange24h,omitempty"`
	// Unpriced marks a cached authoritative null: Stellar Expert doesn't
	// know the asset, rejected its id, or reports no usable price.
	Unpriced bool `json:"unpriced,omitempty"`
}

// GetPrices fetches a snapshot for each canonical token id. The returned map
// is keyed by canonical id; nil values mean the token is unpriceable
// (unknown to Stellar Expert, malformed, or unavailable within the request's
// miss-fetch budget). The whole request only fails on caller context
// cancellation or unrecoverable system errors.
func (p *pricesService) GetPrices(ctx context.Context, tokens []string, network string) (_ map[string]*types.PriceEntry, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(p.svcMetrics, pricesServiceName, "GetPrices", network, time.Since(start).Seconds(), err)
	}()

	if network != types.PUBLIC && network != types.TESTNET {
		return nil, fmt.Errorf("unsupported network for prices: %s", network)
	}
	cacheNet := strings.ToLower(network)

	canonical := utils.DedupePreserveOrder(tokens)
	result := make(map[string]*types.PriceEntry, len(canonical))
	var resultMu sync.Mutex

	cacheKeys := make([]string, len(canonical))
	tokenByCacheKey := make(map[string]string, len(canonical))
	for i, c := range canonical {
		cacheKeys[i] = cacheKey(cacheNet, c)
		tokenByCacheKey[cacheKeys[i]] = c
	}

	for token, entry := range p.loadCachedPrices(ctx, cacheKeys, tokenByCacheKey, network) {
		result[token] = entry
	}

	misses := missingTokens(canonical, result)
	if len(misses) == 0 {
		return result, nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, p.cfg.MissFetchTimeout)
	defer cancel()

	p.resolveMisses(fetchCtx, network, cacheNet, misses, result, &resultMu)
	unresolved := len(missingTokens(canonical, result))
	if unresolved > 0 && fetchCtx.Err() != nil && ctx.Err() == nil {
		logger.Warn("prices: miss fetch budget exhausted; returning best-effort results", "network", network, "misses", len(misses), "unresolved", unresolved)
		if p.pricesMetrics != nil {
			p.pricesMetrics.MissBudgetExhausted.WithLabelValues(network).Inc()
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	completeMissingResults(canonical, result)
	return result, nil
}

// loadCachedPrices returns the cached entries for cacheKeys. Redis expiry
// (CacheTTL) governs freshness, so every entry MGET returns is a live hit and
// any absent key is a miss.
func (p *pricesService) loadCachedPrices(ctx context.Context, cacheKeys []string, tokenByCacheKey map[string]string, network string) map[string]*types.PriceEntry {
	hits := make(map[string]*types.PriceEntry, len(cacheKeys))
	if p.redis == nil {
		p.recordCacheOutcome(network, "miss", len(cacheKeys))
		return hits
	}

	cached, mgetErr := p.redis.MGetJSON(ctx, cacheKeys, func() any { return new(cachedPriceEntry) })
	if mgetErr != nil {
		logger.Warn("prices: redis MGet failed; bypassing cache", "error", mgetErr)
		if p.pricesMetrics != nil {
			p.pricesMetrics.RedisErrors.WithLabelValues("mget").Inc()
		}
		p.recordCacheOutcome(network, "miss", len(cacheKeys))
		return hits
	}

	for _, k := range cacheKeys {
		v, present := cached[k]
		entry, _ := v.(*cachedPriceEntry)
		if !present || entry == nil {
			p.recordCacheOutcome(network, "miss", 1)
			continue
		}
		if entry.Unpriced {
			// A cached null: the token stays in the result map as an
			// explicit nil so it is not re-fetched as a miss. It is counted
			// as its own outcome, never as a "hit" — otherwise a mass
			// negative-caching incident (an upstream wobble turning every
			// token unpriceable) would make the cache dashboards improve
			// while the product broke.
			hits[tokenByCacheKey[k]] = nil
			p.recordCacheOutcome(network, "negative_hit", 1)
			continue
		}
		hits[tokenByCacheKey[k]] = &types.PriceEntry{
			CurrentPrice:             entry.CurrentPrice,
			PercentagePriceChange24h: entry.PercentagePriceChange24h,
		}
		p.recordCacheOutcome(network, "hit", 1)
	}
	return hits
}

func (p *pricesService) recordCacheOutcome(network, outcome string, n int) {
	if p.pricesMetrics == nil || n <= 0 {
		return
	}
	p.pricesMetrics.CacheOutcomes.WithLabelValues(network, outcome).Add(float64(n))
}

func (p *pricesService) resolveMisses(ctx context.Context, network, cacheNet string, misses []string, result map[string]*types.PriceEntry, resultMu *sync.Mutex) {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(p.cfg.MaxConcurrent)

	for _, canonical := range misses {
		g.Go(func() error {
			entry, resolved := p.fetchAndCache(gctx, network, cacheNet, canonical)
			if resolved {
				resultMu.Lock()
				result[canonical] = entry
				resultMu.Unlock()
			}
			// A single token's transient failure must not cancel its siblings;
			// cancellation only propagates from the parent ctx via gctx.
			return nil
		})
	}
	_ = g.Wait()
}

// fetchOutcome is the singleflight-shared result of one upstream fetch.
type fetchOutcome struct {
	entry    *types.PriceEntry
	resolved bool
}

// fetchAndCache returns the priced entry for one canonical asset id, coalescing
// concurrent requests for the same cache key through singleflight so a hot
// token issues a single upstream fetch. The boolean reports whether the token
// was authoritatively resolved for this request: not-found/malformed assets
// resolve to (nil, true); transient failures and budget exhaustion return
// (nil, false). The caller's ctx only bounds how long this request waits — the
// shared fetch runs under its own budget so one caller's cancellation can't
// poison other in-flight waiters.
func (p *pricesService) fetchAndCache(ctx context.Context, network, cacheNet, canonical string) (*types.PriceEntry, bool) {
	ch := p.fetchGroup.DoChan(cacheKey(cacheNet, canonical), func() (any, error) {
		fctx, cancel := context.WithTimeout(context.Background(), p.cfg.MissFetchTimeout)
		defer cancel()
		entry, resolved := p.fetchFromUpstream(fctx, network, cacheNet, canonical)
		return fetchOutcome{entry: entry, resolved: resolved}, nil
	})
	select {
	case <-ctx.Done():
		return nil, false
	case res := <-ch:
		out, _ := res.Val.(fetchOutcome)
		return out.entry, out.resolved
	}
}

// fetchFromUpstream performs the actual Stellar Expert fetch for one canonical
// asset id and writes a successful result to Redis. The asset and candles
// calls run concurrently; on a terminal asset error the candles call is
// cancelled so unknown assets don't double upstream load.
func (p *pricesService) fetchFromUpstream(ctx context.Context, network, cacheNet, canonical string) (_ *types.PriceEntry, resolved bool) {
	stellarExpertID := assetid.ToStellarExpert(canonical)

	// Truncate to the candle resolution so `from` and `to` align to bucket
	// boundaries; otherwise upstream may return a window 23–25h wide with
	// no consistent rule. The current price is still as-of-now via
	// /asset/{id}, so the actual price comparison is at most one bucket off
	// 24h — ~15m at the 900s default, and wider only if an operator raises
	// the resolution.
	resolutionSec := p.cfg.CandlesResolutionSec
	resolution := time.Duration(resolutionSec) * time.Second
	to := time.Now().UTC().Truncate(resolution)
	from := to.Add(-candlesWindow)

	fetchCtx, cancelFetch := context.WithCancel(ctx)
	defer cancelFetch()

	var (
		asset      *types.StellarExpertAsset
		assetErr   error
		candles    []types.StellarExpertCandle
		candlesErr error
		wg         sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		asset, assetErr = p.stellarExpert.GetAsset(fetchCtx, network, stellarExpertID)
		if assetErr != nil && (errors.Is(assetErr, ErrAssetNotFound) || errors.Is(assetErr, ErrAssetMalformed)) {
			cancelFetch()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		candles, candlesErr = p.stellarExpert.GetAssetCandles(fetchCtx, network, stellarExpertID, from, to, resolutionSec)
	}()
	wg.Wait()

	if assetErr != nil {
		if errors.Is(assetErr, ErrAssetNotFound) || errors.Is(assetErr, ErrAssetMalformed) {
			p.cacheNegative(ctx, cacheNet, canonical)
			return nil, true
		}
		if errors.Is(assetErr, context.DeadlineExceeded) || errors.Is(assetErr, context.Canceled) {
			return nil, false
		}
		logger.Warn("prices: upstream fetch failed", "asset", canonical, "error", assetErr)
		return nil, false
	}

	// A zero price — whether Stellar Expert omitted the `price` field (a known
	// but illiquid asset; JSON absence decodes to 0) or reported a genuine 0 —
	// is unpriceable. Honor the documented null contract rather than leaking a
	// "0" string. Resolves to (nil, true) like not-found/malformed so it caches
	// as an authoritative miss.
	if asset.Price == 0 {
		p.cacheNegative(ctx, cacheNet, canonical)
		return nil, true
	}

	// The 24h change comes only from the candles window, which can pin
	// a true trailing 24h (±1h). When candles are unavailable or can't cover
	// ~24h we return null rather than a mislabeled day-over-day delta from the
	// daily price7d series.
	var change24h *string
	if candlesErr != nil {
		if !errors.Is(candlesErr, context.DeadlineExceeded) && !errors.Is(candlesErr, context.Canceled) &&
			!errors.Is(candlesErr, ErrAssetNotFound) && !errors.Is(candlesErr, ErrAssetMalformed) {
			logger.Warn("prices: candles fetch failed; 24h change unavailable", "asset", stellarExpertID, "error", candlesErr)
		}
	} else {
		change24h = change24hFromCandles(asset.Price, candles, to)
	}

	entry := &types.PriceEntry{
		CurrentPrice:             formatPrice(asset.Price),
		PercentagePriceChange24h: change24h,
	}
	p.cachePositive(ctx, cacheNet, canonical, entry)
	return entry, true
}

// change24hFromCandles computes the 24h percentage delta between currentPrice
// and the close of the oldest candle — the first point the chart plots, so
// the list row, detail header, and 1D chart share one anchor (D8).
// Returns nil when the upstream is empty, the close is zero, or the oldest
// returned candle is too far from 24h before `to` to credibly represent a
// 24h window (sparse trading or anomalous upstream return).
func change24hFromCandles(currentPrice float64, candles []types.StellarExpertCandle, to time.Time) *string {
	if len(candles) == 0 {
		return nil
	}
	oldestAge := to.Sub(time.Unix(candles[0].TS(), 0))
	if oldestAge < minCandleWindow || oldestAge > maxCandleWindow {
		return nil
	}
	startPrice := candles[0].Close()
	if startPrice == 0 {
		return nil
	}
	percentChange := (currentPrice - startPrice) / startPrice * 100
	rounded := math.Round(percentChange*100) / 100
	if rounded == 0 {
		// Collapse negative zero to "0" so the JSON is byte-stable.
		rounded = 0
	}
	formatted := strconv.FormatFloat(rounded, 'f', -1, 64)
	return &formatted
}

func (p *pricesService) cachePositive(ctx context.Context, cacheNet, canonical string, entry *types.PriceEntry) {
	if p.redis == nil {
		return
	}
	value := cachedPriceEntry{
		CurrentPrice:             entry.CurrentPrice,
		PercentagePriceChange24h: entry.PercentagePriceChange24h,
	}
	if err := p.redis.SetJSON(ctx, cacheKey(cacheNet, canonical), value, p.cfg.CacheTTL); err != nil {
		logger.Warn("prices: redis SET failed", "asset", canonical, "error", err)
		if p.pricesMetrics != nil {
			p.pricesMetrics.RedisErrors.WithLabelValues("set").Inc()
		}
	}
}

// cacheNegative stores an unpriceable marker at the (configurable) negative
// TTL so unpriceable tokens are not re-fetched on every poll cycle.
// Transient transport failures and 5xx never reach this path.
func (p *pricesService) cacheNegative(ctx context.Context, cacheNet, canonical string) {
	if p.redis == nil {
		return
	}
	if err := p.redis.SetJSON(ctx, cacheKey(cacheNet, canonical), cachedPriceEntry{Unpriced: true}, p.cfg.NegativeCacheTTL); err != nil {
		logger.Warn("prices: redis SET (negative) failed", "asset", canonical, "error", err)
		if p.pricesMetrics != nil {
			p.pricesMetrics.RedisErrors.WithLabelValues("set").Inc()
		}
	}
}

func cacheKey(cacheNet, canonical string) string {
	return cacheKeyPrefix + ":" + cacheNet + ":" + canonical
}

// formatPrice emits the shortest decimal string that round-trips a float64.
// Avoids scientific notation so client-side BigNumber parsers see only
// fixed-point representations.
func formatPrice(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func missingTokens(tokens []string, result map[string]*types.PriceEntry) []string {
	misses := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := result[token]; !ok {
			misses = append(misses, token)
		}
	}
	return misses
}

// completeMissingResults fills nil entries for any token that was neither a
// cache hit nor resolved upstream, so the response carries an explicit
// unpriceable marker for every requested token.
func completeMissingResults(tokens []string, result map[string]*types.PriceEntry) {
	for _, token := range tokens {
		if _, ok := result[token]; !ok {
			result[token] = nil
		}
	}
}
