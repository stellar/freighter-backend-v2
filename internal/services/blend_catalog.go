// ABOUTME: Blend market-catalog service: pool and earn-option views, with
// ABOUTME: per-network caching and earn-pool curation. Earn options are derived
// ABOUTME: from the pools catalog (wallet-backend serves no earn query).
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/store"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	blendCatalogServiceName = "blend-catalog"

	defaultCatalogCacheTTL = 60 * time.Second

	blendPoolsCacheKeyPrefix = "blend:pools:v1"
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

// GetEarnOptions derives the earn catalog from the pools catalog: one entry
// per asset with at least one enabled reserve in a pool whose status accepts
// deposits, filtered through the operator allowlist. It reads through
// GetPools, so both endpoints share one upstream query and one cache entry
// per network; the derivation itself is cheap enough to run per request.
func (b *blendCatalogService) GetEarnOptions(ctx context.Context, network string) (_ *types.BlendEarnOptionsCatalog, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(b.svcMetrics, blendCatalogServiceName, "GetEarnOptions", network, time.Since(start).Seconds(), err)
	}()

	catalog, err := b.GetPools(ctx, network)
	if err != nil {
		return nil, err
	}

	return &types.BlendEarnOptionsCatalog{
		Options: deriveEarnOptions(catalog.Pools, b.allowlist[strings.ToUpper(network)]),
	}, nil
}

func mapCatalogPools(pools []wbtypes.BlendPool) []types.BlendCatalogPool {
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
				SupplyAPY:          r.SupplyApy,
				BorrowAPY:          r.BorrowApy,
				EmissionsSupplyAPR: r.EmissionsSupplyApr,
				SuppliedUSD:        r.SuppliedUsd,
				BorrowedUSD:        r.BorrowedUsd,
				PriceUSD:           r.PriceUsd,
			})
		}
		out = append(out, types.BlendCatalogPool{
			ID:          p.Address,
			Name:        p.Name,
			Status:      (*string)(p.Status),
			SuppliedUSD: p.SuppliedUsd,
			BorrowedUSD: p.BorrowedUsd,
			InterestAPY: p.InterestApy,
			NetAPY:      p.NetApy,
			Reserves:    reserves,
		})
	}
	return out
}

// deriveEarnOptions groups supply-eligible reserves asset-first: pools whose
// status accepts deposits (per the SDK's status table; null status means the
// pool's config is not yet ingested and is excluded), reserves that are
// enabled, and — when an allowlist is configured — pools Freighter curates.
// Assets are ordered by asset id; each asset's pools by supplied USD
// descending (unpriced last, id tie-break), mirroring the ordering the
// upstream earn query used before it was removed.
func deriveEarnOptions(pools []types.BlendCatalogPool, allowed map[string]bool) []types.BlendEarnAssetOption {
	byAsset := make(map[string]*types.BlendEarnAssetOption)
	for _, pool := range pools {
		if pool.Status == nil || !wbtypes.BlendPoolStatus(*pool.Status).AcceptsSupply() {
			continue
		}
		if allowed != nil && !allowed[pool.ID] {
			continue
		}
		for _, r := range pool.Reserves {
			if !r.Enabled {
				continue
			}
			option, ok := byAsset[r.AssetID]
			if !ok {
				option = &types.BlendEarnAssetOption{
					AssetID:  r.AssetID,
					Symbol:   r.Symbol,
					Name:     r.Name,
					Decimals: r.Decimals,
					Pools:    []types.BlendEarnPool{},
				}
				byAsset[r.AssetID] = option
			}
			option.Pools = append(option.Pools, types.BlendEarnPool{
				ID:                 pool.ID,
				Name:               pool.Name,
				SupplyAPY:          r.SupplyAPY,
				EmissionsSupplyAPR: r.EmissionsSupplyAPR,
				SuppliedUSD:        r.SuppliedUSD,
			})
		}
	}

	options := make([]types.BlendEarnAssetOption, 0, len(byAsset))
	for _, option := range byAsset {
		sort.Slice(option.Pools, func(i, j int) bool {
			a, b := option.Pools[i], option.Pools[j]
			switch {
			case a.SuppliedUSD == nil && b.SuppliedUSD == nil:
				return a.ID < b.ID
			case a.SuppliedUSD == nil:
				return false
			case b.SuppliedUSD == nil:
				return true
			case *a.SuppliedUSD != *b.SuppliedUSD:
				return *a.SuppliedUSD > *b.SuppliedUSD
			}
			return a.ID < b.ID
		})
		options = append(options, *option)
	}
	sort.Slice(options, func(i, j int) bool { return options[i].AssetID < options[j].AssetID })
	return options
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
