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

	cacheKeyPrefix = "prices:v1"

	// MaxTokensPerPriceRequest is the hard ceiling for --max-tokens-per-request.
	// One request fans out one upstream Stellar Expert call per cache miss, so
	// the cap bounds the per-request amplification factor regardless of how
	// the operator sets the runtime flag.
	MaxTokensPerPriceRequest = 1000
)

// PricesServiceConfig tunes the orchestrator. Zero values fall back to safe
// defaults so callers can construct a service with PricesServiceConfig{}.
type PricesServiceConfig struct {
	CacheTTL      time.Duration
	MaxConcurrent int
}

type pricesService struct {
	expert     types.StellarExpertService
	redis      *store.RedisStore
	cfg        PricesServiceConfig
	svcMetrics *metrics.Service
}

// NewPricesService wires the orchestrator. redis may be nil; if so, every
// request bypasses the cache and hits Stellar Expert.
func NewPricesService(expert types.StellarExpertService, redis *store.RedisStore, cfg PricesServiceConfig, m *metrics.Service) types.PricesService {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultMaxConcurrent
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaultCacheTTL
	}
	return &pricesService{expert: expert, redis: redis, cfg: cfg, svcMetrics: m}
}

func (p *pricesService) Name() string { return pricesServiceName }

func (p *pricesService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	return p.expert.GetHealth(ctx, network)
}

// cachedPriceEntry is the on-disk shape in Redis. Only positive results are
// cached; unknown / malformed assets are returned as nil per request and
// re-fetched on the next call so freshly-listed assets surface quickly.
type cachedPriceEntry struct {
	CurrentPrice             string  `json:"currentPrice,omitempty"`
	PercentagePriceChange24h *string `json:"percentagePriceChange24h,omitempty"`
}

// GetPrices fetches a snapshot for each canonical token id. The returned map
// is keyed by canonical id; nil values mean the token is unpriceable
// (unknown to Stellar Expert, malformed, or upstream failure). The whole
// request only fails on context cancellation or unrecoverable system errors.
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

	keys := make([]string, len(canonical))
	keyToken := make(map[string]string, len(canonical))
	for i, c := range canonical {
		keys[i] = cacheKey(cacheNet, c)
		keyToken[keys[i]] = c
	}

	if p.redis != nil {
		hits, mgetErr := p.redis.MGetJSON(ctx, keys, func() any { return new(cachedPriceEntry) })
		if mgetErr != nil {
			logger.Warn("prices: redis MGet failed; bypassing cache", "error", mgetErr)
		}
		for k, v := range hits {
			entry, ok := v.(*cachedPriceEntry)
			if !ok {
				continue
			}
			result[keyToken[k]] = &types.PriceEntry{
				CurrentPrice:             entry.CurrentPrice,
				PercentagePriceChange24h: entry.PercentagePriceChange24h,
			}
		}
	}

	misses := make([]string, 0, len(canonical))
	for _, c := range canonical {
		if _, ok := result[c]; !ok {
			misses = append(misses, c)
		}
	}
	if len(misses) == 0 {
		return result, nil
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(p.cfg.MaxConcurrent)

	for _, c := range misses {
		c := c
		g.Go(func() error {
			entry := p.fetchAndCache(gctx, network, cacheNet, c)
			resultMu.Lock()
			result[c] = entry
			resultMu.Unlock()
			return nil
		})
	}

	g.Wait()
	// Per-goroutine errors are swallowed (logged + nil entry), so g.Wait()
	// itself never returns. Surface ctx cancellation / deadline-exceeded
	// explicitly so the handler can map it to 503 instead of 500.
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

// fetchAndCache hits Stellar Expert for one canonical asset id, writes a
// successful result to Redis, and returns the response entry. Per-token
// failures (not found, malformed, transient upstream error) map to nil and
// are not cached; nothing here propagates to the caller.
func (p *pricesService) fetchAndCache(ctx context.Context, network, cacheNet, canonical string) *types.PriceEntry {
	expertID := assetid.ToStellarExpert(canonical)
	asset, err := p.expert.GetAsset(ctx, network, expertID)
	if err != nil {
		if errors.Is(err, ErrAssetNotFound) || errors.Is(err, ErrAssetMalformed) {
			return nil
		}
		logger.Warn("prices: upstream fetch failed", "asset", canonical, "error", err)
		return nil
	}

	entry := &types.PriceEntry{
		CurrentPrice:             formatPrice(asset.Price),
		PercentagePriceChange24h: compute24hChange(asset.Price, asset.Price7d),
	}
	p.cachePositive(ctx, cacheNet, canonical, entry)
	return entry
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
func compute24hChange(current float64, candles [][2]float64) *string {
	if len(candles) < 2 {
		return nil
	}
	prior := candles[len(candles)-2][1]
	if prior == 0 {
		return nil
	}
	pct := (current - prior) / prior * 100
	rounded := math.Round(pct*100) / 100
	if rounded == 0 {
		// Collapse negative zero to "0" so the JSON is byte-stable for tiny
		// downward drifts ("-0" surprises clients that string-compare).
		rounded = 0
	}
	s := strconv.FormatFloat(rounded, 'f', -1, 64)
	return &s
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
