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

	// historyCacheKeyPrefix carries the cached-entry SCHEMA version. It
	// rotated v1→v2 when cachedSeries gained the fetch-time `to` the
	// coverage guard is evaluated against: a v1 entry has no `to`, so a
	// mixed-fleet read of one would measure coverage against the zero
	// timestamp. A cold cache on deploy is what the segment is for.
	historyCacheKeyPrefix = "pricehistory:v2"
	// tokenStatsCacheKeyPrefix rotated v1→v2 when cachedAssetMeta gained the
	// not-found marker: a v1 reader decodes {"notFound":true} as a payload
	// with every field zeroed rather than as "upstream doesn't know this
	// asset".
	tokenStatsCacheKeyPrefix = "tokenstats:v2"

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

// DefaultRangeCacheTTL returns the §6.2 default series cache TTL for one
// range enum member (0 for a non-member). The serve command's flag defaults
// read it rather than repeating the numbers, so the config surface and the
// service cannot drift apart on what "the default" is.
func DefaultRangeCacheTTL(r string) time.Duration {
	return priceHistoryRanges[r].defaultCacheTTL
}

// DefaultRangeCacheTTLSeconds is DefaultRangeCacheTTL in whole seconds, the
// unit the *_SECONDS config surface uses.
func DefaultRangeCacheTTLSeconds(r string) int {
	return int(DefaultRangeCacheTTL(r) / time.Second)
}

// ValidCandleResolutionsSec is upstream's closed `resolution` enum, measured
// in Appendix A.1 — everything outside it returns 400. The set is irregular
// (4h and 12h are valid, 3h/6h/8h are not; 1d and 3d are valid, 2d is not),
// so it cannot be derived from a rule and is hard-coded from that sweep.
// Config that feeds a resolution upstream is validated against this at boot,
// because the alternative is every candles call 400ing in production.
var ValidCandleResolutionsSec = []int{300, 900, 1800, 3600, 7200, 14400, 43200, 86400, 259200, 604800, 1209600}

// IsValidCandleResolutionSec reports whether sec is a member of upstream's
// resolution enum.
func IsValidCandleResolutionSec(sec int) bool {
	for _, v := range ValidCandleResolutionsSec {
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
	// enum member. Zero/absent entries keep DefaultRangeCacheTTL.
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
	// TokenStatsCacheTTL is the TTL of the tokenstats:v2 asset-payload cache
	// entry shared by the volume verdict and the token-stats endpoint.
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
		LowVolume:         s.volumeVerdict(ctx, meta, metaErr, network),
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
	// newest trade and is exactly the point D8 anchors the delta's other
	// end against. Truncation bought nothing in return: the cache key
	// contains no timestamp, so aligned windows never shared an entry.
	to := time.Now().UTC()

	var from time.Time
	if spec.window > 0 {
		from = to.Add(-spec.window)
	} else {
		// ALL: start at the floor. It previously refined this to
		// max(asset.created, floor) by waiting on the asset payload, which
		// bought a narrower upstream window and nothing else — the API
		// returns only buckets that exist, so both windows yield the
		// IDENTICAL series. That made the asset call a blocking dependency
		// of the candles call purely for request tidiness, and it had to be
		// sub-budgeted so a hanging /asset could not starve the chart it was
		// decorating. Since upstream request width is not a cost we are
		// managing, the floor is used directly and candles are issued with
		// no upstream call ahead of them.
		from = time.Unix(allRangeFromFloor, 0).UTC()
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

// cachedAssetMeta is the on-disk shape of one tokenstats:v2 entry — the
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
	// NotFound marks an authoritative "upstream does not know this asset".
	// Caching it follows the emptySeriesCacheTTL reasoning: a 404 asset is
	// the common case for unpriced SEP-41 tokens, and without this every
	// history and stats request for one hits the paid GetAsset endpoint.
	NotFound bool `json:"notFound,omitempty"`
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
// for one canonical id. Positive payloads cache at TokenStatsCacheTTL;
// authoritative not-founds cache as a marker at the short
// emptySeriesCacheTTL. Transient failures are never cached.
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
			// The emptySeriesCacheTTL rationale, at that same short TTL.
			if errors.Is(err, ErrAssetNotFound) || errors.Is(err, ErrAssetMalformed) {
				s.cacheAssetMeta(fctx, key, cachedAssetMeta{NotFound: true}, emptySeriesCacheTTL)
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

func (s *priceHistoryService) cacheAssetMeta(ctx context.Context, key string, value cachedAssetMeta, ttl time.Duration) {
	if s.redis == nil {
		return
	}
	if err := s.redis.SetJSON(ctx, key, value, ttl); err != nil {
		logger.Warn("price-history: redis SET failed", "key", key, "error", err)
		if s.pricesMetrics != nil {
			s.pricesMetrics.RedisErrors.WithLabelValues("set").Inc()
		}
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
// the same reason. It previously evaluated false, which asserted "we checked
// and this token is fine" for every token on every response while no check
// was possible — a claim the service could not support, and the one value the
// tri-state offers no way to walk back. Three things gate the conversion and
// all are open (§13): whether the raw scale is flat or per-asset (a flat ÷1e7
// would permanently flag an 18-decimal token, and a single scalar divisor
// cannot express a per-asset scale), whether the field even counts AMM venues,
// and the absence of any unit-independent proxy to cross-check against. Until
// they close, "unknown" is the honest answer, and the eventual rollout then
// reads as null → true|false — new information arriving — rather than
// false → true, which looks like the token changed.
//
// A zero MinVolume7dUSD is different and stays false: that is an operator
// deliberately turning the banner off, so the check did run and nothing is
// flagged.
func (s *priceHistoryService) volumeVerdict(ctx context.Context, meta *types.StellarExpertAsset, metaErr error, network string) *bool {
	if metaErr != nil || meta == nil {
		// The metric means "upstream degraded". A caller that walked away
		// mid-request also aborts the meta wait, with a context error, but
		// nothing upstream failed — counting it would make an
		// upstream-health signal track client behaviour instead. The
		// verdict is null either way; only the counter is withheld.
		if !isCallerCancellation(ctx, metaErr) && s.pricesMetrics != nil {
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
	verdict := false
	if s.cfg.MinVolume7dUSD > 0 {
		verdict = meta.Volume7d/s.cfg.Volume7dConversionDivisor < s.cfg.MinVolume7dUSD
	}
	return &verdict
}

// isCallerCancellation reports whether err is this request's own context
// ending rather than something upstream failing. Both surface as
// context.Canceled/DeadlineExceeded, so the caller's ctx state is what
// distinguishes them.
func isCallerCancellation(ctx context.Context, err error) bool {
	if ctx.Err() == nil {
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
