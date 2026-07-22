// ABOUTME: Positions service: maps wallet-backend Blend positions into the
// ABOUTME: frontend-shaped account positions response, with per-address caching.
package services

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/store"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	positionsServiceName = "positions"

	defaultPositionsCacheTTL = 30 * time.Second

	// positionsCacheKeyPrefix versions the cached response shape; bump on
	// breaking changes so stale entries die at the key level.
	positionsCacheKeyPrefix = "blend:positions:v1"
)

type positionsService struct {
	walletBackend types.WalletBackendService
	redis         *store.RedisStore
	cacheTTL      time.Duration
	svcMetrics    *metrics.Service
}

// NewPositionsService wires the positions view. redis may be nil; every
// request then bypasses the cache and hits wallet-backend.
func NewPositionsService(walletBackend types.WalletBackendService, redis *store.RedisStore, cacheTTL time.Duration, m *metrics.Service) types.PositionsService {
	if cacheTTL <= 0 {
		cacheTTL = defaultPositionsCacheTTL
	}
	return &positionsService{
		walletBackend: walletBackend,
		redis:         redis,
		cacheTTL:      cacheTTL,
		svcMetrics:    m,
	}
}

func (p *positionsService) Name() string { return positionsServiceName }

// GetAccountPositions returns the account's positions, cached per
// (network, address) for cacheTTL. User-visible staleness is the TTL plus
// wallet-backend's own ingestion lag.
func (p *positionsService) GetAccountPositions(ctx context.Context, address, network string) (_ *types.AccountPositions, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(p.svcMetrics, positionsServiceName, "GetAccountPositions", network, time.Since(start).Seconds(), err)
	}()

	cacheKey := fmt.Sprintf("%s:%s:%s", positionsCacheKeyPrefix, strings.ToLower(network), address)
	if p.redis != nil {
		hits, cacheErr := p.redis.MGetJSON(ctx, []string{cacheKey}, func() any { return &types.AccountPositions{} })
		if cacheErr != nil {
			// Cache trouble must not fail the request; fall through to upstream.
			logger.ErrorWithContext(ctx, "positions cache read failed", "error", cacheErr)
		} else if hit, ok := hits[cacheKey].(*types.AccountPositions); ok {
			return hit, nil
		}
	}

	upstream, err := p.walletBackend.GetBlendPositions(ctx, address, network)
	if err != nil {
		return nil, err
	}

	result := mapAccountPositions(upstream)

	if p.redis != nil {
		if cacheErr := p.redis.SetJSON(ctx, cacheKey, result, p.cacheTTL); cacheErr != nil {
			logger.ErrorWithContext(ctx, "positions cache write failed", "error", cacheErr)
		}
	}
	return result, nil
}

// mapAccountPositions shapes the upstream Blend positions into the response.
func mapAccountPositions(upstream *types.BlendAccountPositions) *types.AccountPositions {
	positions := make([]types.PoolPosition, 0, len(upstream.Pools))
	for _, pool := range upstream.Pools {
		positions = append(positions, types.PoolPosition{
			Protocol:    "blend",
			ID:          pool.PoolAddress,
			Name:        pool.PoolName,
			NetUSD:      pool.USDValue,
			SuppliedUSD: pool.SuppliedUSD,
			BorrowedUSD: pool.BorrowedUSD,
			NetAPY:      pool.NetAPY,
			Blend:       mapBlendDetail(pool.Reserves),
		})
	}

	total, netAPY := accountAggregate(upstream.Pools)
	return &types.AccountPositions{
		TotalValueUSD: total,
		NetAPY:        netAPY,
		Positions:     positions,
	}
}

// mapBlendDetail turns reserve positions into display rows. Reserves with no
// balance on a side produce no row for that side; upstream deliberately
// emits fully-exited (all-zero) reserve rows to carry earnings history, and
// those are filtered here.
func mapBlendDetail(reserves []types.BlendReservePosition) *types.BlendPositionDetail {
	detail := &types.BlendPositionDetail{
		Supply: []types.BlendSupplyRow{},
		Borrow: []types.BlendBorrowRow{},
	}
	for _, r := range reserves {
		supplied := parseRawAmount(r.SuppliedTokens)
		collateral := parseRawAmount(r.CollateralTokens)
		borrowed := parseRawAmount(r.BorrowedTokens)

		if supplied.Sign() > 0 || collateral.Sign() > 0 {
			total := new(big.Int).Add(supplied, collateral)
			detail.Supply = append(detail.Supply, types.BlendSupplyRow{
				AssetID:           r.AssetContractID,
				Symbol:            r.TokenSymbol,
				Name:              r.TokenName,
				Decimals:          r.TokenDecimals,
				SuppliedTokens:    supplied.String(),
				CollateralTokens:  collateral.String(),
				TotalTokens:       total.String(),
				USDValue:          r.SuppliedUSD,
				APY:               r.SupplyAPY,
				EmissionsAPR:      r.EmissionsAPR,
				InterestEarned:    r.InterestEarned,
				InterestEarnedUSD: tokensToUSD(r.InterestEarned, r.TokenDecimals, r.PriceUSD),
				ClaimableBLND:     r.EmissionsEarnedBLND,
				ClaimableUSD:      r.EmissionsEarnedUSD,
				PriceUSD:          r.PriceUSD,
			})
		}
		if borrowed.Sign() > 0 {
			detail.Borrow = append(detail.Borrow, types.BlendBorrowRow{
				AssetID:        r.AssetContractID,
				Symbol:         r.TokenSymbol,
				Name:           r.TokenName,
				Decimals:       r.TokenDecimals,
				BorrowedTokens: borrowed.String(),
				USDValue:       r.BorrowedUSD,
				APY:            r.BorrowAPY,
				PriceUSD:       r.PriceUSD,
			})
		}
	}
	return detail
}

// accountAggregate computes the header figures from the per-pool summaries.
//
// TotalValueUSD: Σ pool usdValue with strict null propagation (any
// unavailable pool value nulls the total — an undercounted "total" is worse
// than an honest null), mirroring upstream's convention for pool totals.
// 0 for an account with no pools.
//
// NetAPY: mean of pool netApy weighted by pool usdValue — the weight basis
// that makes rate × base reproduce the per-pool dollar earnings under the
// upstream's current netApy definition. Null when any input is unavailable
// or the weighted base is zero. Both rules are pending confirmation with
// the wallet-backend team; each is isolated here so a decision lands as a
// one-line change.
func accountAggregate(pools []types.BlendPoolPosition) (total *float64, netAPY *float64) {
	if len(pools) == 0 {
		zero := 0.0
		return &zero, nil
	}

	sum := 0.0
	apyNumerator := 0.0
	apyKnown := true
	for _, pool := range pools {
		if pool.USDValue == nil {
			return nil, nil
		}
		sum += *pool.USDValue
		if pool.NetAPY == nil {
			apyKnown = false
			continue
		}
		apyNumerator += *pool.NetAPY * *pool.USDValue
	}

	total = &sum
	if apyKnown && sum != 0 {
		apy := apyNumerator / sum
		if !math.IsInf(apy, 0) && !math.IsNaN(apy) {
			netAPY = &apy
		}
	}
	return total, netAPY
}

// parseRawAmount parses an upstream raw-unit token amount. Upstream declares
// these non-null integer strings; anything unparseable is treated as zero so
// one bad row cannot fail the whole response.
func parseRawAmount(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return big.NewInt(0)
	}
	return v
}

// tokensToUSD converts a raw-unit token amount to USD at the given price.
// Null when decimals or price are unavailable — never a fabricated zero.
func tokensToUSD(rawAmount string, decimals *int32, priceUSD *float64) *float64 {
	if decimals == nil || priceUSD == nil {
		return nil
	}
	raw, ok := new(big.Float).SetString(rawAmount)
	if !ok {
		return nil
	}
	scale := new(big.Float).SetFloat64(math.Pow10(int(*decimals)))
	tokens := new(big.Float).Quo(raw, scale)
	usd, _ := new(big.Float).Mul(tokens, big.NewFloat(*priceUSD)).Float64()
	return &usd
}
