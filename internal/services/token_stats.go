package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// defaultSupplyDecimals is the scale applied when the asset payload omits
// `decimals` (classic assets and XLM — Appendix A.2).
const defaultSupplyDecimals = 7

// PriceHistoryAndStatsService is what NewPriceHistoryService returns: one
// implementation serves both the history and stats endpoints because they
// share the tokenstats:v1-cached asset payload — one upstream asset call
// feeds the volume verdict, the ALL-range `from`, and the stats rows.
type PriceHistoryAndStatsService interface {
	types.PriceHistoryService
	types.TokenStatsService
}

// GetTokenStats returns the sourceable stats rows for one canonical token
// id, read off the same cached asset payload the history service fetches.
// Absent/unsourceable fields are omitted entirely (D4) — no nulls, no zeros.
// An unknown asset yields an empty stats block, not an error.
func (s *priceHistoryService) GetTokenStats(ctx context.Context, canonical, network string) (_ *types.TokenStats, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(s.svcMetrics, priceHistoryServiceName, "GetTokenStats", network, time.Since(start).Seconds(), err)
	}()

	if network != types.PUBLIC && network != types.TESTNET {
		return nil, fmt.Errorf("unsupported network for token stats: %s", network)
	}
	cacheNet := strings.ToLower(network)

	meta, err := s.getAssetMeta(ctx, network, cacheNet, canonical)
	if err != nil {
		if errors.Is(err, ErrAssetNotFound) || errors.Is(err, ErrAssetMalformed) {
			// No rows — clients hide the section. 200, never 404.
			return &types.TokenStats{}, nil
		}
		return nil, err
	}

	stats := &types.TokenStats{}
	decimals := defaultSupplyDecimals
	if meta.Decimals != nil {
		decimals = *meta.Decimals
	}
	if supply, ok := scaleSupplyByDecimals(meta.Supply.String(), decimals); ok {
		stats.SupplyOnStellar = &supply
	}
	if meta.Trustlines.Funded != nil {
		holders := *meta.Trustlines.Funded
		stats.Holders = &holders
	}
	return stats, nil
}

// scaleSupplyByDecimals converts the raw integer supply string into its
// decimal representation by shifting the decimal point left by `decimals`
// digits — pure string arithmetic, because real supplies (19+ digits) exceed
// float64's exact-integer range and may exceed int64 for high-decimal
// SEP-41 tokens. Anything that is not a plain unsigned integer is reported
// unsourceable (ok=false) so the row is omitted rather than emitted wrong.
func scaleSupplyByDecimals(raw string, decimals int) (string, bool) {
	if raw == "" || decimals < 0 {
		return "", false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return "", false
		}
	}

	if decimals == 0 {
		return raw, true
	}

	intPart, fracPart := "0", raw
	if len(raw) > decimals {
		intPart, fracPart = raw[:len(raw)-decimals], raw[len(raw)-decimals:]
	} else {
		fracPart = strings.Repeat("0", decimals-len(raw)) + raw
	}
	fracPart = strings.TrimRight(fracPart, "0")
	if fracPart == "" {
		return intPart, true
	}
	return intPart + "." + fracPart, true
}
