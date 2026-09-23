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

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils/assetid"
)

// Design doc for the price-history and token-stats work, referenced by the
// markers throughout this package:
//
//	stellar/wallet-eng-monorepo → design-docs/token-price-graphs/token-price-graphs-design.md
//
//	Dn    a numbered decision from the doc's Decisions table. Stable — an id is
//	      assigned once and is never renumbered. Prefer this form.
//	A.n   a measured fact from the Stellar Expert appendix, recorded from a real
//	      sweep rather than derived. Appendix letters are stable in practice.
//	§n.n  a section number. LEAST stable: inserting a section renumbers every
//	      reference below it, and nothing verifies these. Do not add new ones —
//	      inline the reasoning, or cite a Dn.
//
// A marker points at reasoning that code cannot express; it is never a source of
// truth. Where a doc claim is load-bearing it is pinned by a test and the marker
// is omitted, so a drift between doc and code fails CI rather than misleading a
// reader. The doc lives in another repo, so no commit can update both atomically.

const (
	priceHistoryServiceName = "price-history"

	// The segment is the cached-entry SCHEMA version. Bump it when the
	// on-disk shape changes in a way an older reader would misread AND that
	// reader can still be running somewhere shared — `prices:v2` is the
	// worked example, where a v1 reader takes an unpriced entry for a real
	// price. Both of these stay at v1 because no shared environment has
	// ever run this branch.
	//
	// Flush a sandbox Redis before bisecting back across this branch: older
	// builds read these same keys with a narrower struct, and a
	// `{"notFound":true}` entry decodes there as a found asset with every
	// value zeroed.
	historyCacheKeyPrefix    = "pricehistory:v1"
	tokenStatsCacheKeyPrefix = "tokenstats:v1"

	historyCurrency = "USD"

	// emptySeriesCacheTTL is the flat negative-cache TTL for empty series
	// (§6.2). Unpriced contract tokens are the common SEP-41 case; without
	// this, every open of one would reach upstream uncacheably.
	emptySeriesCacheTTL = 15 * time.Minute

	defaultTokenStatsCacheTTL = time.Hour

	// allRangeFromFloor is 2015-09-01 — the `from` every ALL-range request
	// starts at. It predates the network, and the API returns only buckets
	// that exist, so a window this wide returns exactly the same series a
	// per-asset start date would.
	allRangeFromFloor = int64(1441065600)

	// coverageGuardRatio is §4.4 rule 5: the delta is withheld when the
	// series covers less than ~90% of the requested window. 1D keeps the
	// prices path's 23-25h band verbatim; ALL is exempt.
	coverageGuardRatio = 0.9

	allRange = "ALL"
	oneDay   = "1D"
)

// rangeSpec is one row of the range→request map (Appendix A.1). Every
// resolution is itself a valid upstream enum member, so no range relies on
// the silent-coarsening behavior — but the response resolution is still
// always derived from the returned timestamps, never assumed.
type rangeSpec struct {
	resolutionSec   int64
	window          time.Duration // 0 → ALL: from = allRangeFromFloor
	defaultCacheTTL time.Duration
}

// Past roughly 200 buckets the API silently COARSENS rather than rejecting
// (§3 fact 2), so check the record count before changing a resolution here.
// ALL exceeds 200 and is safe only because 2w is the coarsest valid
// resolution — nothing left to coarsen to, so the full series comes back.
// That does not generalise: a new range at a sub-2w resolution over a long
// window would be coarsened, and would depend on undocumented behaviour.
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

// DefaultRangeCacheTTLSeconds returns the §6.2 default series cache TTL for
// one range enum member, in the whole seconds the *_SECONDS config surface
// uses (0 for a non-member). The serve command's flag defaults read it
// rather than repeating the numbers, so the config surface and the service
// cannot drift apart on what "the default" is.
func DefaultRangeCacheTTLSeconds(r string) int {
	return int(priceHistoryRanges[r].defaultCacheTTL / time.Second)
}

// validCandleResolutionsSec is upstream's closed `resolution` enum, measured
// in Appendix A.1 — everything outside it returns 400. The set is irregular
// (4h and 12h are valid, 3h/6h/8h are not; 1d and 3d are valid, 2d is not),
// so it cannot be derived from a rule and is hard-coded from that sweep.
//
// Nothing is validated against it at boot any more — no resolution is
// configurable, so there is no operator input left to check. Its job now is
// to pin the two places resolutions are chosen IN CODE: the priceHistoryRanges
// table and candlesResolutionSec. TestCandleResolutionsAreUpstreamMembers
// asserts both, because a plausible-looking edit to the table (2d = 172800
// sits right between the valid 1d and 3d) would otherwise 400 every candles
// call for that range in production with nothing to catch it first.
var validCandleResolutionsSec = []int{300, 900, 1800, 3600, 7200, 14400, 43200, 86400, 259200, 604800, 1209600}

// isValidCandleResolutionSec reports whether sec is a member of upstream's
// resolution enum.
func isValidCandleResolutionSec(sec int) bool {
	for _, v := range validCandleResolutionsSec {
		if v == sec {
			return true
		}
	}
	return false
}

// PriceHistoryServiceConfig tunes the history/stats orchestrator. Zero values
// fall back to safe defaults so callers can construct a service with
// PriceHistoryServiceConfig{}.
type PriceHistoryServiceConfig struct {
	// CacheTTLs overrides the per-range series cache TTLs, keyed by range
	// enum member. Zero/absent entries keep the range's default TTL.
	CacheTTLs map[string]time.Duration
	// FetchTimeout bounds each upstream fetch (shared singleflight budget).
	FetchTimeout time.Duration
	// MinVolume7dUSD is the §4.4 rule 2 warning threshold in USD. 0 disables
	// the guard.
	MinVolume7dUSD float64
	// Volume7dConversionDivisor converts the raw upstream volume7d into USD
	// (raw ÷ divisor). The units are UNCONFIRMED (§13), so the default 0
	// means "conversion not enabled", which makes the verdict null: with no
	// conversion there is no check to pass. Enabling the guard is a config
	// change, never a code change. A failed asset lookup is null too.
	Volume7dConversionDivisor float64
	// TokenStatsCacheTTL is the TTL of the tokenstats:v1 asset-payload cache
	// entry shared by the volume verdict and the token-stats endpoint.
	TokenStatsCacheTTL time.Duration
}

// The one implementation satisfies both endpoint interfaces, asserted here so
// a drift fails in this package rather than only where api wires it up.
var (
	_ types.PriceHistoryService = (*priceHistoryService)(nil)
	_ types.TokenStatsService   = (*priceHistoryService)(nil)
)

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
//
// The single return value satisfies both types.PriceHistoryService and
// types.TokenStatsService: one implementation serves both endpoints because
// they share the tokenstats:v1-cached asset payload — one upstream asset
// call feeds the volume verdict and the stats rows. Callers assign it to
// whichever of the two interfaces they need.
func NewPriceHistoryService(stellarExpert types.StellarExpertService, redis JSONCache, prices types.PricesService, cfg PriceHistoryServiceConfig, metricsService *metrics.Service, pricesMetrics *metrics.Prices) *priceHistoryService {
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
	// own cache and singleflight key. The asset call feeds only the volume
	// verdict now, so nothing in the series path waits on it: its failure
	// yields lowVolume: null while the series still returns. Deliberately
	// NOT the prices service's cancel-candles-on-asset-not-found behavior —
	// here candles are the payload (B.3).
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
	// Fetched unconditionally even though the shipped config leaves the
	// volume verdict disabled (divisor 0), so volumeVerdict discards this.
	// It is not wasted: getAssetMeta reads and writes the SAME
	// tokenstats:v1 entry /token-stats reads, and the token detail view
	// calls both endpoints. Gating this on the divisor would only move the
	// cold fetch onto the stats call a moment later, off the concurrent
	// path and onto a serial one.
	go func() {
		defer wg.Done()
		meta, metaErr = s.getAssetMeta(ctx, network, cacheNet, canonical)
	}()
	// Spot joins the fan-out rather than running after it. The delta's two
	// inputs are independent — the series comes from /candles, the spot from
	// the prices service — and each carries its own multi-second fetch
	// budget, so serially they stack to roughly twice the http.Server
	// WriteTimeout and the connection dies before any status line is
	// written.
	//
	// KNOWN COST, measured not estimated: on a COLD token this request
	// issues 2x GetAsset and 2x GetAssetCandles upstream, not 1x each. The
	// prices service resolves its own asset+candles under prices:v2 and its
	// own singleflight group, while getAssetMeta above resolves the asset
	// under tokenstats:v1 and this service's group — different keys,
	// different groups, so nothing coalesces them. Warm, the caches absorb
	// it: a request where only the 30s spot has expired issues ONE of each
	// (measured), because getSeries and getAssetMeta are still served from
	// pricehistory:v1 and tokenstats:v1. The duplication recurs at THOSE
	// boundaries instead — the series TTL (15m on 1D) and the stats TTL
	// (1h), not the spot's 30s.
	//
	// Not fixed here because the obvious fix — having the history service
	// derive spot from the asset payload it already fetched — re-couples
	// the two formulas D8 exists to keep identical, and the inverse (prices
	// reading the history cache) is the dependency cycle this design
	// deliberately avoids. Flagged for a maintainer call rather than
	// silently restructured.
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
		LowVolume:         s.volumeVerdict(ctx, meta, metaErr, network),
		Change:            computeChange(historyRange, spec, points, got.to, spot, spotOK),
		Points:            points,
	}, nil
}

// cachedSeries is the on-disk shape of one pricehistory:v1 entry. Redis
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
		// A caller who navigated away cancels this ctx, and the Redis client
		// surfaces that verbatim. Counting it would move a Redis-health
		// signal with client behaviour — the same line isCallerCancellation
		// and VolumeVerdictNull draw. The cache is simply bypassed either way.
		reportRedisFailure(ctx, s.pricesMetrics, "mget", "price-history: redis MGet failed; bypassing cache", err)
		return series{}, false
	}
	entry, _ := cached[key].(*cachedSeries)
	if entry == nil {
		return series{}, false
	}
	// A non-positive `To` means the entry predates this field. Treat it as
	// a MISS rather than trusting it: time.Unix(0, 0) is 1970, so a
	// reconstructed `to` would make coverageOK measure a ~55-year NEGATIVE
	// oldestAge, fail every band, and null `change` for the rest of the
	// entry's TTL — up to 7 days on ALL. A miss refetches and rewrites the
	// entry in the current shape, so the condition self-heals on first read.
	//
	// This is reachable because these keys ship at v1 rather than rotating:
	// no shared environment ever ran this branch (dev runs a main commit),
	// but a personal sandbox built from an earlier commit of it would have
	// written entries with no `to` at all.
	if entry.To <= 0 {
		return series{}, false
	}
	out := series{points: entry.Points}
	if out.points == nil {
		out.points = make([]types.PricePoint, 0)
	}
	out.to = time.Unix(entry.To, 0).UTC()
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
	// newest trade and is exactly the point D8 anchors the delta's other
	// end against. Truncation bought nothing in return: the cache key
	// contains no timestamp, so aligned windows never shared an entry.
	to := time.Now().UTC()

	var from time.Time
	if spec.window > 0 {
		// `from` IS truncated, and has to be for a reason `to` does not
		// share. Upstream returns the buckets whose start is >= `from`, so
		// an unaligned `from` lands mid-bucket and the series silently
		// begins one bucket late. That shifts the candle every delta
		// anchors on (computeChange reads points[0]), and it breaks D8: the
		// prices path derives `from` by subtracting 24h from a TRUNCATED
		// `to`, so its window opens exactly on a bucket boundary. Two
		// windows one bucket apart produce two different "24h changes" for
		// the same asset — the detail header disagreeing with the list row,
		// which is the whole thing D8 exists to prevent. Truncating here
		// reproduces the prices path's boundary without also rounding `to`
		// back and losing the live bucket.
		//
		// Plain `%` rather than a Euclidean modulo: `to` is always now and
		// the widest truncated window is a year, so `fromUnix` cannot be
		// negative — and ALL, the only range whose floor is fixed, takes the
		// branch below without truncating at all.
		//
		// Truncated on the Unix timestamp, NOT via time.Time.Truncate:
		// that rounds relative to the zero time (Jan 1, year 1), which is a
		// whole number of days from the epoch but not a whole number of 3d
		// or 2w periods — so it would misalign 1Y and ALL against upstream's
		// epoch-anchored grid while looking correct for the sub-day ranges.
		fromUnix := to.Add(-spec.window).Unix()
		from = time.Unix(fromUnix-fromUnix%spec.resolutionSec, 0).UTC()
	} else {
		// ALL: start at the floor rather than at the asset's creation date.
		// The API returns only buckets that exist, so both windows yield
		// the identical series — narrowing it would make the candles call
		// wait on the asset call for nothing.
		from = time.Unix(allRangeFromFloor, 0).UTC()
	}

	candles, err := s.stellarExpert.GetAssetCandles(ctx, network, assetid.ToStellarExpert(canonical), from, to, int(spec.resolutionSec))
	if err != nil {
		if errors.Is(err, ErrAssetNotFound) || errors.Is(err, ErrAssetMalformed) {
			empty := series{points: make([]types.PricePoint, 0), to: to}
			s.cacheSeries(ctx, key, empty, s.seriesTTL(historyRange, spec, empty.points))
			return empty, nil
		}
		return series{}, err
	}

	points := make([]types.PricePoint, 0, len(candles))
	for _, c := range candles {
		points = append(points, types.PricePoint{T: c.TS(), P: formatPrice(c.Close())})
	}

	fetched := series{points: points, to: to}
	s.cacheSeries(ctx, key, fetched, s.seriesTTL(historyRange, spec, points))
	return fetched, nil
}

// seriesTTL resolves the cache TTL for one fetched series: the range's TTL
// (operator override, else the §6.2 default), with emptySeriesCacheTTL
// applied as a CAP when the series came back empty.
//
// A cap, not an override. 1H's TTL is 5m, so assigning a flat 15m kept an
// empty 1H chart blank for three times the range's own TTL and made
// --price-history-cache-ttl-1h-seconds inert for exactly the case an
// operator would shorten it for. Ranges longer than 15m still shorten, which
// is the SEP-41 negative-caching benefit this constant exists for.
//
// Both empty-series paths go through here — a 200 with zero points, and the
// not-found/malformed answer that fetchSeries maps to an empty series — so
// they cannot drift apart. The second is the more common one: an unpriced
// SEP-41 token 404s by design.
func (s *priceHistoryService) seriesTTL(historyRange string, spec rangeSpec, points []types.PricePoint) time.Duration {
	ttl := spec.defaultCacheTTL
	if override, ok := s.cfg.CacheTTLs[historyRange]; ok && override > 0 {
		ttl = override
	}
	if len(points) == 0 && ttl > emptySeriesCacheTTL {
		ttl = emptySeriesCacheTTL
	}
	return ttl
}

func (s *priceHistoryService) cacheSeries(ctx context.Context, key string, value series, ttl time.Duration) {
	if s.redis == nil {
		return
	}
	wctx, cancel := cacheWriteContext(ctx)
	defer cancel()
	if err := s.redis.SetJSON(wctx, key, cachedSeries{Points: value.points, To: value.to.Unix()}, ttl); err != nil {
		reportRedisFailure(wctx, s.pricesMetrics, "set", "price-history: redis SET failed", err, "key", key)
	}
}

// cachedAssetMeta is the on-disk shape of one tokenstats:v1 entry — the
// asset-payload subset its two readers actually consult: the volume verdict
// (Volume7d) and the token-stats endpoint (Supply, Decimals, Funded).
// Supply is kept as a string because real supplies exceed float64's
// exact-integer range (and an empty json.Number does not marshal).
//
// Deliberately not the whole asset. `price` and `created` were carried here
// once and read by nobody after the ALL-range `from` refinement was dropped,
// which is a cache entry storing fields it cannot answer questions about.
type cachedAssetMeta struct {
	Supply   string `json:"supply,omitempty"`
	Decimals *int   `json:"decimals,omitempty"`
	// Pointer for the same reason StellarExpertAsset.Volume7d is one: a
	// float64 here would turn "upstream never reported volume" back into a
	// genuine zero on the way out of the cache, undoing the distinction.
	Volume7d *float64 `json:"volume7d,omitempty"`
	Funded   *int64   `json:"funded,omitempty"`
	// NotFound marks an authoritative "upstream does not know this asset".
	// Caching it follows the emptySeriesCacheTTL reasoning: a 404 asset is
	// the common case for unpriced SEP-41 tokens, and without this every
	// history and stats request for one hits the paid GetAsset endpoint.
	NotFound bool `json:"notFound,omitempty"`
}

func (m *cachedAssetMeta) toAsset() *types.StellarExpertAsset {
	asset := &types.StellarExpertAsset{
		Decimals: m.Decimals,
		Volume7d: m.Volume7d,
	}
	asset.Supply = json.Number(m.Supply)
	asset.Trustlines.Funded = m.Funded
	return asset
}

func assetToCachedMeta(a *types.StellarExpertAsset) cachedAssetMeta {
	return cachedAssetMeta{
		Supply:   a.Supply.String(),
		Decimals: a.Decimals,
		Volume7d: a.Volume7d,
		Funded:   a.Trustlines.Funded,
	}
}

// getAssetMeta returns the (1h-cached, singleflight-coalesced) asset payload
// for one canonical id. Positive payloads cache at TokenStatsCacheTTL;
// authoritative not-founds cache as a marker at the short
// emptySeriesCacheTTL. Transient failures are never cached.
func (s *priceHistoryService) getAssetMeta(ctx context.Context, network, cacheNet, canonical string) (*types.StellarExpertAsset, error) {
	key := tokenStatsCacheKey(cacheNet, canonical)
	if s.redis != nil {
		cached, err := s.redis.MGetJSON(ctx, []string{key}, func() any { return new(cachedAssetMeta) })
		if err != nil {
			reportRedisFailure(ctx, s.pricesMetrics, "mget", "price-history: redis MGet failed; bypassing asset cache", err)
		} else if entry, _ := cached[key].(*cachedAssetMeta); entry != nil {
			if entry.NotFound {
				// Counted apart from "hit" for the same reason the prices
				// path separates negative_hit: serving cached absence is
				// not the thing hit rate is meant to measure.
				s.recordStatsCacheOutcome(network, "negative_hit")
				return nil, ErrAssetNotFound
			}
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
			// Not-found/malformed are upstream's authoritative answer, and
			// they are the common case for unpriced SEP-41 tokens — without
			// caching them, every open of one pays for a GetAsset call.
			// The emptySeriesCacheTTL rationale, applied as a CAP for the
			// same reason seriesTTL applies it as one: pinning 15m flat
			// would let a negative marker outlive the positive payloads
			// around it whenever an operator shortens
			// --token-stats-cache-ttl-seconds — which is exactly the lever
			// they would reach for to drain poisoned state during an
			// upstream incident.
			if errors.Is(err, ErrAssetNotFound) || errors.Is(err, ErrAssetMalformed) {
				s.cacheAssetMeta(fctx, key, cachedAssetMeta{NotFound: true}, s.negativeMetaTTL())
			}
			return nil, err
		}
		s.cacheAssetMeta(fctx, key, assetToCachedMeta(asset), s.cfg.TokenStatsCacheTTL)
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

// negativeMetaTTL is the TTL for a cached authoritative asset-not-found.
// emptySeriesCacheTTL capped by the configured stats TTL, so the marker can
// never outlive the positive payloads sharing its keyspace.
func (s *priceHistoryService) negativeMetaTTL() time.Duration {
	if s.cfg.TokenStatsCacheTTL > 0 && s.cfg.TokenStatsCacheTTL < emptySeriesCacheTTL {
		return s.cfg.TokenStatsCacheTTL
	}
	return emptySeriesCacheTTL
}

func (s *priceHistoryService) cacheAssetMeta(ctx context.Context, key string, value cachedAssetMeta, ttl time.Duration) {
	if s.redis == nil {
		return
	}
	wctx, cancel := cacheWriteContext(ctx)
	defer cancel()
	if err := s.redis.SetJSON(wctx, key, value, ttl); err != nil {
		reportRedisFailure(wctx, s.pricesMetrics, "set", "price-history: redis SET failed", err, "key", key)
	}
}

// volumeVerdict is the §4.4 rule 2 low-volume verdict. The tri-state answers
// "did the check run?", which is the only reading under which all three
// values are truthful:
//
//	null   the check could not run — no usable volume signal exists
//	false  the check ran and this token passed
//	true   the check ran and this token failed; show the banner
//
// So an unavailable asset payload is null and NEVER false: defaulting to
// false would drop the warning during exactly the upstream degradation where
// a painted price goes unchallenged. That null is counted, so the quadrant
// is operator-visible.
//
// An unconfirmed unit conversion (divisor 0, the shipped default) is null for
// the same reason: a false would assert "we checked and this token is fine"
// while no check was possible, and it is the one value the tri-state offers
// no way to walk back. The unit questions gating the conversion are open
// (§13); until they close, "unknown" is the honest answer, and enabling it
// later reads as null → true|false — new information — rather than
// false → true, which looks like the token changed.
//
// A zero MinVolume7dUSD is different and stays false: that is an operator
// deliberately turning the banner off, so the check did run and nothing is
// flagged.
func (s *priceHistoryService) volumeVerdict(ctx context.Context, meta *types.StellarExpertAsset, metaErr error, network string) *bool {
	if metaErr != nil || meta == nil {
		// The metric means "upstream degraded". Two ways to reach this
		// branch are not that, and the verdict is null for all of them —
		// only the counter is withheld:
		//
		//   - A caller that walked away mid-request aborts the meta wait
		//     with a context error. Nothing upstream failed; counting it
		//     would make an upstream-health signal track client behaviour.
		//   - An authoritative not-found/malformed is upstream ANSWERING,
		//     not failing. For an unpriced SEP-41 token it is the designed
		//     common case — common enough that this service negatively
		//     caches it — so counting it would climb steadily on ordinary
		//     traffic, and a second request serves it from that cache with
		//     no upstream call at all. Same reasoning as the divisor case
		//     below: an upstream-health signal must not be swamped by a
		//     constant.
		//
		// The fleet-wide-404 failure mode (a changed upstream route prefix,
		// a misconfigured base URL) is therefore NOT this counter's job.
		// It surfaces as freighter_service_errors_total volume on
		// error_type="http_error:404", which doJSON labels these with
		// precisely so that signal has somewhere honest to live.
		authoritative := errors.Is(metaErr, ErrAssetNotFound) || errors.Is(metaErr, ErrAssetMalformed)
		if !authoritative && !isCallerCancellation(ctx, metaErr) && s.pricesMetrics != nil {
			s.pricesMetrics.VolumeVerdictNull.WithLabelValues(network).Inc()
		}
		return nil
	}
	if s.cfg.Volume7dConversionDivisor <= 0 {
		// Deliberately NOT counted in VolumeVerdictNull: that counter means
		// "the asset-payload call failed while candles succeeded", and this
		// null is a static config state every response would share. Mixing
		// them would swamp an upstream-health signal with a constant.
		return nil
	}
	if s.cfg.MinVolume7dUSD <= 0 {
		// The operator turned the banner off. No comparison happens, so the
		// answer cannot depend on whether upstream sent a volume — this
		// MUST come before the nil check below, or "guard disabled" silently
		// becomes "unknown" for exactly the tokens with no reading, which is
		// not what --price-history-min-volume-7d-usd=0 advertises.
		disabled := false
		return &disabled
	}
	if meta.Volume7d == nil {
		// No usable volume signal — upstream omitted the field or its shape
		// drifted. The tri-state exists for exactly this: a false here would
		// assert "we checked and this token is fine" on data we never
		// received, and a true would accuse it of manipulation on the same
		// absence. Not counted in VolumeVerdictNull for the reason above —
		// this is upstream answering, not upstream failing.
		return nil
	}
	verdict := *meta.Volume7d/s.cfg.Volume7dConversionDivisor < s.cfg.MinVolume7dUSD
	return &verdict
}

// isCallerCancellation reports whether err is the CALLER walking away rather
// than something upstream failing. Both surface as
// context.Canceled/DeadlineExceeded on the error, so the request ctx's own
// state is what distinguishes them — and only context.Canceled counts.
//
// The distinction is not cosmetic. Every request runs under the handler's 9s
// cap (handlers.TokenPriceHistoryContextTimeout), so when a cached series is
// served while /asset/{id} hangs upstream, it is OUR budget that ends the
// meta wait and ctx.Err() is DeadlineExceeded. That is upstream degradation
// with a client still waiting, and it is precisely what VolumeVerdictNull
// exists to show. Treating it as caller cancellation blanked the metric in
// its own quadrant. The prices service draws the same line in its
// miss-budget check.
func isCallerCancellation(ctx context.Context, err error) bool {
	if !errors.Is(ctx.Err(), context.Canceled) {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// computeChange is the D8 spot-anchored delta: absolute = spot −
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
	// fetchedTo is always set: fetchSeries assigns it, and loadCachedSeries
	// rejects an entry that has no stored `to` instead of reconstructing
	// one. There is deliberately no zero fallback here — the one this
	// replaced tested IsZero(), which is year 1 and so could never have
	// matched the epoch value a missing `to` actually decodes to.
	oldestAge := fetchedTo.Sub(time.Unix(points[0].T, 0))
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
