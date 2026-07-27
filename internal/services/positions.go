// ABOUTME: Positions service: maps wallet-backend Blend positions into the
// ABOUTME: frontend-shaped account positions response.
package services

import (
	"context"
	"math"
	"math/big"
	"time"

	"golang.org/x/sync/errgroup"

	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const (
	positionsServiceName = "positions"

	defaultPositionsConcurrency = 10
)

type positionsService struct {
	walletBackend  types.WalletBackendService
	maxConcurrency int
	svcMetrics     *metrics.Service
}

// NewPositionsService wires the positions view. maxConcurrency caps the
// per-request fan-out goroutines, like the balances fan-out.
func NewPositionsService(walletBackend types.WalletBackendService, maxConcurrency int, m *metrics.Service) types.PositionsService {
	if maxConcurrency <= 0 {
		maxConcurrency = defaultPositionsConcurrency
	}
	return &positionsService{
		walletBackend:  walletBackend,
		maxConcurrency: maxConcurrency,
		svcMetrics:     m,
	}
}

func (p *positionsService) Name() string { return positionsServiceName }

// GetAccountsPositions returns positions for each unique requested address,
// fetched from wallet-backend on every request (like balances): no caching,
// so a fresh deposit is visible as soon as the indexer ingests it. Unknown
// accounts are normal per-address outcomes (empty positions, already
// normalized by the wallet-backend service); any other failure is systemic
// and fails the whole request.
func (p *positionsService) GetAccountsPositions(ctx context.Context, addresses []string, network string) (_ []*types.AccountPositions, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(p.svcMetrics, positionsServiceName, "GetAccountsPositions", network, time.Since(start).Seconds(), err)
	}()

	unique := utils.DedupePreserveOrder(addresses)
	results := make([]*types.AccountPositions, len(unique))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(p.maxConcurrency)
	for i, addr := range unique {
		g.Go(func() error {
			upstream, fetchErr := p.walletBackend.GetBlendPositions(gctx, addr, network)
			if fetchErr != nil {
				return fetchErr
			}
			entry := mapAccountPositions(upstream)
			entry.Address = addr
			results[i] = entry
			return nil
		})
	}
	if err = g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

// mapAccountPositions shapes the upstream Blend positions into the response.
func mapAccountPositions(upstream *wbtypes.BlendAccountPositions) *types.AccountPositions {
	positions := make([]types.PoolPosition, 0, len(upstream.Pools))
	for _, pool := range upstream.Pools {
		positions = append(positions, types.PoolPosition{
			Protocol:    "blend",
			ID:          pool.PoolAddress,
			Name:        pool.PoolName,
			NetUSD:      pool.UsdValue,
			SuppliedUSD: pool.SuppliedUsd,
			BorrowedUSD: pool.BorrowedUsd,
			NetAPY:      pool.NetApy,
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
func mapBlendDetail(reserves []wbtypes.BlendReservePosition) *types.BlendPositionDetail {
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
				USDValue:          r.SuppliedUsd,
				APY:               r.SupplyApy,
				EmissionsAPR:      r.EmissionsSupplyApr,
				InterestEarned:    r.InterestEarned,
				InterestEarnedUSD: tokensToUSD(r.InterestEarned, r.TokenDecimals, r.PriceUsd),
				ClaimableBLND:     r.EmissionsEarnedBlnd,
				ClaimableUSD:      r.EmissionsEarnedUsd,
				PriceUSD:          r.PriceUsd,
			})
		}
		if borrowed.Sign() > 0 {
			detail.Borrow = append(detail.Borrow, types.BlendBorrowRow{
				AssetID:        r.AssetContractID,
				Symbol:         r.TokenSymbol,
				Name:           r.TokenName,
				Decimals:       r.TokenDecimals,
				BorrowedTokens: borrowed.String(),
				USDValue:       r.BorrowedUsd,
				APY:            r.BorrowApy,
				EmissionsAPR:   r.EmissionsBorrowApr,
				PriceUSD:       r.PriceUsd,
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
// NetAPY: mean of pool netApy weighted by pool suppliedUsd — the base the
// upstream rate is defined over (blend-sdk-js: net dollars / total
// supplied), so rate × base reproduces the per-pool dollar earnings. Null
// when any pool's netApy or suppliedUsd is unavailable or the supplied base
// is zero.
func accountAggregate(pools []wbtypes.BlendPoolPosition) (total *float64, netAPY *float64) {
	if len(pools) == 0 {
		zero := 0.0
		return &zero, nil
	}

	sum := 0.0
	suppliedSum := 0.0
	apyNumerator := 0.0
	apyKnown := true
	for _, pool := range pools {
		if pool.UsdValue == nil {
			return nil, nil
		}
		sum += *pool.UsdValue
		if pool.NetApy == nil || pool.SuppliedUsd == nil {
			apyKnown = false
			continue
		}
		apyNumerator += *pool.NetApy * *pool.SuppliedUsd
		suppliedSum += *pool.SuppliedUsd
	}

	total = &sum
	if apyKnown && suppliedSum != 0 {
		apy := apyNumerator / suppliedSum
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
