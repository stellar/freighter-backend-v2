// ABOUTME: Blend market-catalog service: pool and earn-option views from
// ABOUTME: wallet-backend, with per-network caching and earn-pool curation.
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/store"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	blendCatalogServiceName = "blend-catalog"

	defaultCatalogCacheTTL = 60 * time.Second

	blendPoolsCacheKeyPrefix = "blend:pools:v1"
	blendEarnCacheKeyPrefix  = "blend:earn:v1"
)

// earnPoolsAllowlist maps network name (PUBLIC/TESTNET) to the set of pool
// contract addresses Freighter offers in the Earn flow. It curates the
// earn-options endpoint only: the pools catalog and user positions are never
// filtered, since users may hold positions in non-curated pools. A nil
// allowlist (no config file) disables curation.
type earnPoolsAllowlist map[string]map[string]bool

// loadEarnPoolsAllowlist reads the JSON allowlist:
//
//	{"PUBLIC": ["CPOOL..."], "TESTNET": ["CPOOL..."]}
//
// An empty path returns nil (curation disabled). A missing or malformed
// file is a startup error: silently serving every pool when the operator
// configured a curated list would be worse than failing fast.
func loadEarnPoolsAllowlist(path string) (earnPoolsAllowlist, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading earn pools allowlist %s: %w", path, err)
	}
	var raw map[string][]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing earn pools allowlist %s: %w", path, err)
	}
	allowlist := make(earnPoolsAllowlist, len(raw))
	for network, pools := range raw {
		set := make(map[string]bool, len(pools))
		for _, pool := range pools {
			set[pool] = true
		}
		allowlist[strings.ToUpper(network)] = set
	}
	return allowlist, nil
}

type blendCatalogService struct {
	walletBackend types.WalletBackendService
	redis         *store.RedisStore
	cacheTTL      time.Duration
	allowlist     earnPoolsAllowlist
	svcMetrics    *metrics.Service
}

// NewBlendCatalogService wires the market views. redis may be nil (no
// caching); allowlistPath may be empty (no earn curation).
func NewBlendCatalogService(walletBackend types.WalletBackendService, redis *store.RedisStore, cacheTTL time.Duration, allowlistPath string, m *metrics.Service) (types.BlendCatalogService, error) {
	allowlist, err := loadEarnPoolsAllowlist(allowlistPath)
	if err != nil {
		return nil, err
	}
	if cacheTTL <= 0 {
		cacheTTL = defaultCatalogCacheTTL
	}
	return &blendCatalogService{
		walletBackend: walletBackend,
		redis:         redis,
		cacheTTL:      cacheTTL,
		allowlist:     allowlist,
		svcMetrics:    m,
	}, nil
}

func (b *blendCatalogService) Name() string { return blendCatalogServiceName }

// GetPools returns the unfiltered pool catalog, cached per network.
func (b *blendCatalogService) GetPools(ctx context.Context, network string) (_ *types.BlendPoolsCatalog, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(b.svcMetrics, blendCatalogServiceName, "GetPools", network, time.Since(start).Seconds(), err)
	}()

	cacheKey := fmt.Sprintf("%s:%s", blendPoolsCacheKeyPrefix, strings.ToLower(network))
	if cached, ok := cacheGet[types.BlendPoolsCatalog](ctx, b.redis, cacheKey); ok {
		return cached, nil
	}

	pools, err := b.walletBackend.GetBlendPools(ctx, network)
	if err != nil {
		return nil, err
	}

	result := &types.BlendPoolsCatalog{Pools: mapCatalogPools(pools)}
	cacheSet(ctx, b.redis, cacheKey, result, b.cacheTTL)
	return result, nil
}

// GetEarnOptions returns the earn catalog, allowlist-filtered and cached per
// network (the cache stores the post-filter result).
func (b *blendCatalogService) GetEarnOptions(ctx context.Context, network string) (_ *types.BlendEarnOptionsCatalog, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(b.svcMetrics, blendCatalogServiceName, "GetEarnOptions", network, time.Since(start).Seconds(), err)
	}()

	cacheKey := fmt.Sprintf("%s:%s", blendEarnCacheKeyPrefix, strings.ToLower(network))
	if cached, ok := cacheGet[types.BlendEarnOptionsCatalog](ctx, b.redis, cacheKey); ok {
		return cached, nil
	}

	options, err := b.walletBackend.GetBlendEarnOptions(ctx, network)
	if err != nil {
		return nil, err
	}

	result := &types.BlendEarnOptionsCatalog{Options: mapEarnOptions(options, b.allowlist[strings.ToUpper(network)])}
	cacheSet(ctx, b.redis, cacheKey, result, b.cacheTTL)
	return result, nil
}

func mapCatalogPools(pools []types.BlendPool) []types.BlendCatalogPool {
	out := make([]types.BlendCatalogPool, 0, len(pools))
	for _, p := range pools {
		reserves := make([]types.BlendCatalogReserve, 0, len(p.Reserves))
		for _, r := range p.Reserves {
			reserves = append(reserves, types.BlendCatalogReserve{
				AssetID:            r.AssetContractID,
				Symbol:             r.TokenSymbol,
				Name:               r.TokenName,
				Decimals:           r.TokenDecimals,
				Enabled:            r.Enabled,
				Utilization:        r.Utilization,
				SupplyAPY:          r.SupplyAPY,
				BorrowAPY:          r.BorrowAPY,
				EmissionsSupplyAPR: r.EmissionsSupplyAPR,
				SuppliedUSD:        r.SuppliedUSD,
				BorrowedUSD:        r.BorrowedUSD,
				PriceUSD:           r.PriceUSD,
			})
		}
		out = append(out, types.BlendCatalogPool{
			ID:          p.Address,
			Name:        p.Name,
			Status:      p.Status,
			SuppliedUSD: p.SuppliedUSD,
			BorrowedUSD: p.BorrowedUSD,
			InterestAPY: p.InterestAPY,
			NetAPY:      p.NetAPY,
			Reserves:    reserves,
		})
	}
	return out
}

// mapEarnOptions shapes the earn catalog, dropping pools outside the
// allowlist (when one is configured) and assets left with no pools.
func mapEarnOptions(options []types.BlendEarnOption, allowed map[string]bool) []types.BlendEarnAssetOption {
	out := make([]types.BlendEarnAssetOption, 0, len(options))
	for _, option := range options {
		pools := make([]types.BlendEarnPool, 0, len(option.Pools))
		for _, p := range option.Pools {
			if allowed != nil && !allowed[p.PoolAddress] {
				continue
			}
			pools = append(pools, types.BlendEarnPool{
				ID:                 p.PoolAddress,
				Name:               p.PoolName,
				SupplyAPY:          p.SupplyAPY,
				EmissionsSupplyAPR: p.EmissionsSupplyAPR,
				SuppliedUSD:        p.SuppliedUSD,
			})
		}
		if len(pools) == 0 {
			continue
		}
		out = append(out, types.BlendEarnAssetOption{
			AssetID:  option.AssetContractID,
			Symbol:   option.TokenSymbol,
			Name:     option.TokenName,
			Decimals: option.TokenDecimals,
			Pools:    pools,
		})
	}
	return out
}

// cacheGet fetches and decodes one cached value. Misses and cache errors
// both report ok=false; cache trouble is logged, never fatal.
func cacheGet[T any](ctx context.Context, redis *store.RedisStore, key string) (*T, bool) {
	if redis == nil {
		return nil, false
	}
	hits, err := redis.MGetJSON(ctx, []string{key}, func() any { return new(T) })
	if err != nil {
		logger.ErrorWithContext(ctx, "blend catalog cache read failed", "key", key, "error", err)
		return nil, false
	}
	hit, ok := hits[key].(*T)
	return hit, ok
}

// cacheSet stores one value best-effort; failures are logged and ignored.
func cacheSet(ctx context.Context, redis *store.RedisStore, key string, value any, ttl time.Duration) {
	if redis == nil {
		return
	}
	if err := redis.SetJSON(ctx, key, value, ttl); err != nil {
		logger.ErrorWithContext(ctx, "blend catalog cache write failed", "key", key, "error", err)
	}
}
