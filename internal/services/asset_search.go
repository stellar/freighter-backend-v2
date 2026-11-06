package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/singleflight"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const (
	assetSearchServiceName = "asset-search"

	// assetSearchCacheKeyPrefix carries the cached-entry SCHEMA version, like
	// the prices and history prefixes: bump it if the stored shape changes.
	// An entry is StellarExpert's /asset body as returned.
	assetSearchCacheKeyPrefix = "assetsearch:v1"

	// DefaultAssetSearchCacheTTL is how long one query's answer is reused.
	// StellarExpert bills every call we make, cached on its side or not, so
	// the cost of a query people keep asking is one call per TTL window.
	DefaultAssetSearchCacheTTL = 6 * time.Hour

	// DefaultAssetSearchEmptyCacheTTL is how long an answer with no records
	// is reused. Hours would hide a token searched before StellarExpert
	// indexed it; minutes cost little, since empty answers rarely repeat.
	DefaultAssetSearchEmptyCacheTTL = 5 * time.Minute

	// DefaultAssetSearchMaxLimit caps the page size a client may ask for.
	// Cost grows with it (≈10 + 6×limit units per search, 10 + 4×limit per
	// sorted list: 610 and 410 at 100), and the wallets ask for 50.
	DefaultAssetSearchMaxLimit = 100
)

// AssetSearchServiceConfig tunes the asset search. Zero values fall back to
// the defaults above, never to "no cache" or "no cap".
type AssetSearchServiceConfig struct {
	CacheTTL time.Duration
	// EmptyCacheTTL is how long an answer with no records is reused. It is
	// short so a token searched before StellarExpert indexes it shows up
	// soon after, and it never exceeds CacheTTL.
	EmptyCacheTTL time.Duration
	MaxLimit      int
	// FetchTimeout bounds each upstream call, which runs detached from the
	// request so a caller who leaves does not waste a paid response.
	FetchTimeout time.Duration
}

var _ types.AssetSearchService = (*assetSearchService)(nil)

type assetSearchService struct {
	stellarExpert types.StellarExpertService
	redis         JSONCache
	cfg           AssetSearchServiceConfig
	pricesMetrics *metrics.Prices
	// fetchGroup coalesces concurrent misses per cache key, so a burst of
	// identical queries makes one paid call.
	fetchGroup singleflight.Group
}

// NewAssetSearchService wires the asset search. redis may be nil (every
// request then goes upstream); pricesMetrics may be nil for tests.
func NewAssetSearchService(stellarExpert types.StellarExpertService, redis JSONCache, cfg AssetSearchServiceConfig, pricesMetrics *metrics.Prices) types.AssetSearchService {
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = DefaultAssetSearchCacheTTL
	}
	if cfg.EmptyCacheTTL <= 0 {
		cfg.EmptyCacheTTL = DefaultAssetSearchEmptyCacheTTL
	}
	cfg.EmptyCacheTTL = min(cfg.EmptyCacheTTL, cfg.CacheTTL)
	if cfg.MaxLimit <= 0 {
		cfg.MaxLimit = DefaultAssetSearchMaxLimit
	}
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = defaultMissFetchTTL
	}
	return &assetSearchService{
		stellarExpert: stellarExpert,
		redis:         redis,
		cfg:           cfg,
		pricesMetrics: pricesMetrics,
	}
}

func (s *assetSearchService) Name() string { return assetSearchServiceName }

// SearchAssets answers one /asset query from the cache, or from StellarExpert
// on a miss. Only successful answers are cached, and one without records only
// for EmptyCacheTTL; errors keep their sentinels (ErrUpstreamAuth,
// ErrAssetMalformed) for the handler to map.
func (s *assetSearchService) SearchAssets(ctx context.Context, network string, params url.Values) (json.RawMessage, error) {
	if network != types.PUBLIC && network != types.TESTNET {
		return nil, fmt.Errorf("unsupported network for asset search: %s", network)
	}
	canonical, err := canonicalAssetSearchParams(params, s.cfg.MaxLimit)
	if err != nil {
		return nil, err
	}
	// The key is the exact query sent upstream, so an entry can only ever
	// answer the question it was fetched for.
	key := assetSearchCacheKeyPrefix + ":" + strings.ToLower(network) + ":" + canonical.Encode()

	if body, ok := s.loadCached(ctx, key); ok {
		s.recordCacheOutcome(network, "hit")
		return body, nil
	}
	s.recordCacheOutcome(network, "miss")

	// The shared fetch runs under its own budget, so one caller's
	// cancellation cannot fail the others waiting on it, and a paid
	// response still reaches the cache after its caller has left.
	ch := s.fetchGroup.DoChan(key, func() (any, error) {
		fctx, cancel := context.WithTimeout(context.Background(), s.cfg.FetchTimeout)
		defer cancel()
		body, err := s.stellarExpert.ListAssets(fctx, network, canonical)
		if err != nil {
			return nil, err
		}
		s.store(fctx, key, body)
		return body, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		body, _ := res.Val.(json.RawMessage)
		return body, nil
	}
}

func (s *assetSearchService) loadCached(ctx context.Context, key string) (json.RawMessage, bool) {
	if s.redis == nil {
		return nil, false
	}
	cached, err := s.redis.MGetJSON(ctx, []string{key}, func() any { return new(json.RawMessage) })
	if err != nil {
		reportRedisFailure(ctx, s.pricesMetrics, "mget", "asset-search: redis MGet failed; bypassing cache", err)
		return nil, false
	}
	entry, _ := cached[key].(*json.RawMessage)
	if entry == nil || len(*entry) == 0 {
		return nil, false
	}
	return *entry, true
}

func (s *assetSearchService) store(ctx context.Context, key string, body json.RawMessage) {
	if s.redis == nil {
		return
	}
	ttl := s.cfg.CacheTTL
	if !hasRecords(body) {
		ttl = s.cfg.EmptyCacheTTL
	}
	wctx, cancel := cacheWriteContext(ctx)
	defer cancel()
	if err := s.redis.SetJSON(wctx, key, body, ttl); err != nil {
		reportRedisFailure(wctx, s.pricesMetrics, "set", "asset-search: redis SET failed", err, "key", key)
	}
}

// hasRecords reports whether a StellarExpert /asset body lists at least one
// record. A body without the records list counts as empty, so an unexpected
// shape is not replayed for hours.
func hasRecords(body json.RawMessage) bool {
	var page struct {
		Embedded struct {
			Records []json.RawMessage `json:"records"`
		} `json:"_embedded"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return false
	}
	return len(page.Embedded.Records) > 0
}

func (s *assetSearchService) recordCacheOutcome(network, outcome string) {
	if s.pricesMetrics == nil {
		return
	}
	s.pricesMetrics.AssetSearchCacheOutcomes.WithLabelValues(network, outcome).Inc()
}

// assetSearchParams are the /asset list parameters a client may send. They
// are the ones StellarExpert documents (sort, order, limit, cursor,
// includeUninitialized) plus search, which works and which both wallets use
// but which StellarExpert's v0.3 spec leaves out. Anything else is refused,
// notably estimate: present with any value, it makes upstream answer a price
// quote instead of records.
var assetSearchParams = map[string]bool{
	"search":               true,
	"sort":                 true,
	"order":                true,
	"limit":                true,
	"cursor":               true,
	"includeUninitialized": true,
}

// assetSearchSorts are the sort values StellarExpert's /asset spec lists.
var assetSearchSorts = []string{"rating", "created", "payments", "trades", "trustlines", "volume", "volume7d"}

// maxAssetSearchValueLen bounds every parameter value, in characters, which
// also bounds the cache key built from them. A full contract address is 56
// characters, so 100 leaves room for any real search.
const maxAssetSearchValueLen = 100

// maxEchoedParamLen bounds a client-sent parameter name quoted back in an
// error message.
const maxEchoedParamLen = 40

// ErrInvalidAssetSearchParams marks a query the asset search refuses before
// calling upstream. Handlers map it to 400; the wrapped message says why.
var ErrInvalidAssetSearchParams = errors.New("invalid asset search parameters")

// canonicalAssetSearchParams validates a client's /asset query and returns
// its canonical form. That form is BOTH what is sent upstream and what the
// cache key is built from, so the cache can never answer one query with
// another's result. It rewrites only what StellarExpert treats as equal:
// surrounding spaces, letter case in search text, and a limit above
// maxLimit, which is clamped the way StellarExpert clamps past a plan's
// batch size. Empty values carry nothing and are dropped, and so is
// includeUninitialized=false, which StellarExpert would read as true.
//
// sort, order and includeUninitialized must be values StellarExpert matches
// exactly. It answers anything else with its default rather than an error
// (measured on Testnet: VOLUME7D sorts by rating, ASC sorts descending), so
// a typo would otherwise quietly return a list nobody asked for.
func canonicalAssetSearchParams(in url.Values, maxLimit int) (url.Values, error) {
	out := url.Values{}
	for key, values := range in {
		if !assetSearchParams[key] {
			return nil, fmt.Errorf("%w: unsupported parameter %q", ErrInvalidAssetSearchParams, clip(key, maxEchoedParamLen))
		}
		if len(values) > 1 {
			return nil, fmt.Errorf("%w: %s given more than once", ErrInvalidAssetSearchParams, key)
		}
		if len(values) == 0 {
			continue
		}
		value := strings.TrimSpace(values[0])
		if value == "" {
			continue
		}
		if utf8.RuneCountInString(value) > maxAssetSearchValueLen {
			return nil, fmt.Errorf("%w: %s is longer than %d characters", ErrInvalidAssetSearchParams, key, maxAssetSearchValueLen)
		}
		switch key {
		case "search":
			value = canonicalSearchTerm(value)
		case "limit":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("%w: limit must be a positive integer", ErrInvalidAssetSearchParams)
			}
			value = strconv.Itoa(min(n, maxLimit))
		case "sort":
			if !slices.Contains(assetSearchSorts, value) {
				return nil, fmt.Errorf("%w: sort must be one of %s", ErrInvalidAssetSearchParams, strings.Join(assetSearchSorts, ", "))
			}
		case "order":
			if value != "asc" && value != "desc" {
				return nil, fmt.Errorf("%w: order must be asc or desc", ErrInvalidAssetSearchParams)
			}
		case "includeUninitialized":
			if value != "true" && value != "false" {
				return nil, fmt.Errorf("%w: includeUninitialized must be true or false", ErrInvalidAssetSearchParams)
			}
			if value == "false" {
				// StellarExpert reads any value as true, so false is sent as
				// the parameter's absence.
				continue
			}
		}
		out.Set(key, value)
	}
	return out, nil
}

// canonicalSearchTerm lowercases a search term, except a full account or
// contract address. StellarExpert ignores case for codes and names, but
// matches a full address only in its canonical uppercase form (measured on
// Testnet: an issuer in uppercase returned 14 records, in lowercase none).
func canonicalSearchTerm(term string) string {
	upper := strings.ToUpper(term)
	if utils.IsValidStellarPublicKey(upper) || utils.IsValidContractID(upper) {
		return upper
	}
	return strings.ToLower(term)
}

// clip cuts s to at most n bytes on a UTF-8 boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}
