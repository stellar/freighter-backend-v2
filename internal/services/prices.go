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

	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/store"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils/assetid"
)

const (
	pricesServiceName = "prices"

	defaultMaxConcurrent = 25
	defaultCacheTTL      = 30 * time.Second
	defaultStaleCacheTTL = 2 * time.Minute
	defaultMissFetchTTL  = 9 * time.Second

	cacheKeyPrefix = "prices:v1"

	// MaxTokensPerPriceRequest is the hard ceiling for --max-tokens-per-request.
	// One request fans out one upstream Stellar Expert call per cache miss, so
	// the cap bounds the per-request amplification factor regardless of how
	// the operator sets the runtime flag.
	MaxTokensPerPriceRequest = 1000

	// candlesWindow / candlesResolutionSec define the rolling 24h window used
	// to compute percentagePriceChange24h from /asset/{id}/candles. Hourly
	// resolution yields ~25 records, well under Stellar Expert's 200-record cap.
	candlesWindow         = 24 * time.Hour
	candlesResolutionSec  = 3600
)

// PricesServiceConfig tunes the orchestrator. Zero values fall back to safe
// defaults so callers can construct a service with PricesServiceConfig{}.
type PricesServiceConfig struct {
	CacheTTL         time.Duration
	StaleCacheTTL    time.Duration
	MissFetchTimeout time.Duration
	MaxConcurrent    int
}

type pricesService struct {
	stellarExpert types.StellarExpertService
	redis         *store.RedisStore
	cfg           PricesServiceConfig
	svcMetrics    *metrics.Service
}

// NewPricesService wires the orchestrator. redis may be nil; if so, every
// request bypasses the cache and hits Stellar Expert.
func NewPricesService(stellarExpert types.StellarExpertService, redis *store.RedisStore, cfg PricesServiceConfig, metricsService *metrics.Service) types.PricesService {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultMaxConcurrent
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaultCacheTTL
	}
	if cfg.StaleCacheTTL <= 0 {
		cfg.StaleCacheTTL = defaultStaleCacheTTL
	}
	if cfg.StaleCacheTTL < cfg.CacheTTL {
		cfg.StaleCacheTTL = cfg.CacheTTL
	}
	if cfg.MissFetchTimeout <= 0 {
		cfg.MissFetchTimeout = defaultMissFetchTTL
	}
	return &pricesService{stellarExpert: stellarExpert, redis: redis, cfg: cfg, svcMetrics: metricsService}
}

func (p *pricesService) Name() string { return pricesServiceName }

// cachedPriceEntry is the on-disk shape in Redis. Only positive results are
// cached; FetchedAt lets us distinguish fresh hits from bounded stale fallbacks.
type cachedPriceEntry struct {
	CurrentPrice             string  `json:"currentPrice,omitempty"`
	PercentagePriceChange24h *string `json:"percentagePriceChange24h,omitempty"`
	FetchedAt                string  `json:"fetchedAt,omitempty"`
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

	canonical := dedupePreserveOrder(tokens)
	result := make(map[string]*types.PriceEntry, len(canonical))
	var resultMu sync.Mutex

	cacheKeys := make([]string, len(canonical))
	tokenByCacheKey := make(map[string]string, len(canonical))
	for i, c := range canonical {
		cacheKeys[i] = cacheKey(cacheNet, c)
		tokenByCacheKey[cacheKeys[i]] = c
	}

	freshHits, staleFallback := p.loadCachedPrices(ctx, cacheKeys, tokenByCacheKey, start)
	for token, entry := range freshHits {
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
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	completeMissingResults(canonical, result, staleFallback)
	return result, nil
}

func (p *pricesService) loadCachedPrices(ctx context.Context, cacheKeys []string, tokenByCacheKey map[string]string, now time.Time) (map[string]*types.PriceEntry, map[string]*types.PriceEntry) {
	fresh := make(map[string]*types.PriceEntry, len(cacheKeys))
	stale := make(map[string]*types.PriceEntry, len(cacheKeys))
	if p.redis == nil {
		return fresh, stale
	}

	hits, mgetErr := p.redis.MGetJSON(ctx, cacheKeys, func() any { return new(cachedPriceEntry) })
	if mgetErr != nil {
		logger.Warn("prices: redis MGet failed; bypassing cache", "error", mgetErr)
		return fresh, stale
	}

	for k, v := range hits {
		entry, ok := v.(*cachedPriceEntry)
		if !ok {
			continue
		}
		priceEntry := &types.PriceEntry{
			CurrentPrice:             entry.CurrentPrice,
			PercentagePriceChange24h: entry.PercentagePriceChange24h,
		}
		switch entry.freshness(now, p.cfg.CacheTTL, p.cfg.StaleCacheTTL) {
		case cacheFresh:
			fresh[tokenByCacheKey[k]] = priceEntry
		case cacheStale:
			stale[tokenByCacheKey[k]] = priceEntry
		}
	}
	return fresh, stale
}

func (p *pricesService) resolveMisses(ctx context.Context, network, cacheNet string, misses []string, result map[string]*types.PriceEntry, resultMu *sync.Mutex) {
	workers := min(len(misses), p.cfg.MaxConcurrent)
	if workers == 0 {
		return
	}

	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case canonical, ok := <-jobs:
					if !ok {
						return
					}
					entry, resolved := p.fetchAndCache(ctx, network, cacheNet, canonical)
					if !resolved {
						continue
					}
					resultMu.Lock()
					result[canonical] = entry
					resultMu.Unlock()
				}
			}
		}()
	}

	for _, canonical := range misses {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case jobs <- canonical:
		}
	}
	close(jobs)
	wg.Wait()
}

// fetchAndCache hits Stellar Expert for one canonical asset id, writes a
// successful result to Redis, and returns the response entry. The boolean
// reports whether the token was authoritatively resolved for this request:
// not-found/malformed assets resolve to nil; transient failures and budget
// exhaustion leave the token eligible for stale fallback.
func (p *pricesService) fetchAndCache(ctx context.Context, network, cacheNet, canonical string) (_ *types.PriceEntry, resolved bool) {
	stellarExpertID := assetid.ToStellarExpert(canonical)
	asset, err := p.stellarExpert.GetAsset(ctx, network, stellarExpertID)
	if err != nil {
		if errors.Is(err, ErrAssetNotFound) || errors.Is(err, ErrAssetMalformed) {
			return nil, true
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, false
		}
		logger.Warn("prices: upstream fetch failed", "asset", canonical, "error", err)
		return nil, false
	}

	change24h := p.compute24hChangeFromCandles(ctx, network, stellarExpertID, asset.Price)
	if change24h == nil {
		// Fallback for native XLM (candles are empty) and for transient
		// /candles failures: derive from the daily price7d on /asset/{id}.
		change24h = compute24hChange(asset.Price, asset.Price7d)
	}

	entry := &types.PriceEntry{
		CurrentPrice:             formatPrice(asset.Price),
		PercentagePriceChange24h: change24h,
	}
	p.cachePositive(ctx, cacheNet, canonical, entry)
	return entry, true
}

// compute24hChangeFromCandles fetches a rolling 24h hourly candle window and
// returns the percentage delta between the current price and the open of the
// oldest candle. Returns nil when the upstream is empty (e.g. native XLM has
// no candles), the request fails, or the open is zero.
func (p *pricesService) compute24hChangeFromCandles(ctx context.Context, network, stellarExpertID string, currentPrice float64) *string {
	to := time.Now().UTC()
	from := to.Add(-candlesWindow)
	candles, err := p.stellarExpert.GetAssetCandles(ctx, network, stellarExpertID, from, to, candlesResolutionSec)
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) &&
			!errors.Is(err, ErrAssetNotFound) && !errors.Is(err, ErrAssetMalformed) {
			logger.Warn("prices: candles fetch failed; falling back to price7d", "asset", stellarExpertID, "error", err)
		}
		return nil
	}
	if len(candles) == 0 {
		return nil
	}
	openPrice := candles[0].Open()
	if openPrice == 0 {
		return nil
	}
	percentChange := (currentPrice - openPrice) / openPrice * 100
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
		FetchedAt:                time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := p.redis.SetJSON(ctx, cacheKey(cacheNet, canonical), value, p.cfg.StaleCacheTTL); err != nil {
		logger.Warn("prices: redis SET failed", "asset", canonical, "error", err)
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

// compute24hChange returns the percentage delta between the latest reported
// price and the candle one day prior. Returns nil when there is insufficient
// history or the prior price is zero (avoids divide-by-zero).
func compute24hChange(currentPrice float64, dailyCandles [][2]float64) *string {
	if len(dailyCandles) < 2 {
		return nil
	}
	priorPrice := dailyCandles[len(dailyCandles)-2][1]
	if priorPrice == 0 {
		return nil
	}
	percentChange := (currentPrice - priorPrice) / priorPrice * 100
	rounded := math.Round(percentChange*100) / 100
	if rounded == 0 {
		// Collapse negative zero to "0" so the JSON is byte-stable for tiny
		// downward drifts ("-0" surprises clients that string-compare).
		rounded = 0
	}
	formatted := strconv.FormatFloat(rounded, 'f', -1, 64)
	return &formatted
}

type cacheFreshness int

const (
	cacheMissing cacheFreshness = iota
	cacheFresh
	cacheStale
)

func (c *cachedPriceEntry) freshness(now time.Time, freshTTL, staleTTL time.Duration) cacheFreshness {
	if c == nil || c.FetchedAt == "" {
		return cacheMissing
	}
	fetchedAt, err := time.Parse(time.RFC3339Nano, c.FetchedAt)
	if err != nil {
		return cacheMissing
	}
	age := now.Sub(fetchedAt)
	if age < 0 {
		age = 0
	}
	if age <= freshTTL {
		return cacheFresh
	}
	if age <= staleTTL {
		return cacheStale
	}
	return cacheMissing
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

func completeMissingResults(tokens []string, result, staleFallback map[string]*types.PriceEntry) {
	for _, token := range tokens {
		if _, ok := result[token]; ok {
			continue
		}
		if fallback, ok := staleFallback[token]; ok {
			result[token] = fallback
			continue
		}
		result[token] = nil
	}
}

func dedupePreserveOrder(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}
