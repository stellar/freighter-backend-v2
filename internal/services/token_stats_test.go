package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func newStatsService(expert types.StellarExpertService, cache JSONCache, cfg PriceHistoryServiceConfig) *priceHistoryService {
	return NewPriceHistoryService(expert, cache, &utils.MockPricesService{}, cfg, nil, nil)
}

func assetWithStats(price float64, supply string, decimals *int, funded *int64) *types.StellarExpertAsset {
	asset := &types.StellarExpertAsset{Price: price, Supply: json.Number(supply)}
	asset.Decimals = decimals
	asset.Trustlines.Funded = funded
	return asset
}

func intPtr(v int) *int             { return &v }
func int64Ptr(v int64) *int64       { return &v }
func float64Ptr(v float64) *float64 { return &v }

// The A.2 XLM fixture: supply is a 19-digit raw integer, decimals absent
// (default 7), holders = trustlines.funded.
func TestTokenStats_XLM(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.Set("XLM", assetWithStats(0.1604, "1054439020873472865", nil, int64Ptr(9926520)))

	svc := newStatsService(expert, nil, PriceHistoryServiceConfig{})
	got, err := svc.GetTokenStats(context.Background(), "XLM", types.PUBLIC)
	require.NoError(t, err)

	require.NotNil(t, got.SupplyOnStellar)
	assert.Equal(t, "105443902087.3472865", *got.SupplyOnStellar)
	require.NotNil(t, got.Holders)
	assert.Equal(t, int64(9926520), *got.Holders)
}

func TestTokenStats_ExplicitDecimals(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.Set("USDC-"+testIssuer+"-1", assetWithStats(1.000007, "4349718972397050", intPtr(7), int64Ptr(665679)))

	svc := newStatsService(expert, nil, PriceHistoryServiceConfig{})
	got, err := svc.GetTokenStats(context.Background(), "USDC:"+testIssuer, types.PUBLIC)
	require.NoError(t, err)

	require.NotNil(t, got.SupplyOnStellar)
	assert.Equal(t, "434971897.239705", *got.SupplyOnStellar)
	require.NotNil(t, got.Holders)
	assert.Equal(t, int64(665679), *got.Holders)
}

// D4: unsourceable/absent fields are OMITTED — never emitted as nulls or
// zeros. The wire assertion is on the marshaled JSON, not just the struct.
func TestTokenStats_AbsentFieldsOmittedEntirely(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.Set("BARE-"+testIssuer+"-1", &types.StellarExpertAsset{Price: 0.5})

	svc := newStatsService(expert, nil, PriceHistoryServiceConfig{})
	got, err := svc.GetTokenStats(context.Background(), "BARE:"+testIssuer, types.PUBLIC)
	require.NoError(t, err)

	assert.Nil(t, got.SupplyOnStellar)
	assert.Nil(t, got.Holders)

	raw, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "supplyOnStellar")
	assert.NotContains(t, string(raw), "holders")
	assert.NotContains(t, string(raw), "null")
	assert.NotContains(t, string(raw), "volume7d", "no volume row until units are confirmed")
}

// An unknown asset yields an empty stats block (clients hide the section),
// not an error and not a 404.
func TestTokenStats_NotFoundIsEmptyStats(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	svc := newStatsService(expert, nil, PriceHistoryServiceConfig{})
	got, err := svc.GetTokenStats(context.Background(), "GHOST:"+testIssuer, types.PUBLIC)
	require.NoError(t, err)
	assert.Nil(t, got.SupplyOnStellar)
	assert.Nil(t, got.Holders)
}

// A 404 from the asset endpoint is authoritative — upstream is telling us it
// does not know this asset — and it is the common case for unpriced SEP-41
// tokens. Without negative caching, every history and stats request for one
// hits the paid GetAsset endpoint forever. The emptySeriesCacheTTL rationale
// applies unchanged, at that same short TTL.
func TestTokenStats_AuthoritativeNotFoundIsNegativelyCached(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert() // GHOST unknown → ErrAssetNotFound
	cache := newFakeJSONCache()
	reg := prometheus.NewRegistry()
	pm := metrics.NewPrices(reg)
	svc := NewPriceHistoryService(expert, cache, &utils.MockPricesService{}, PriceHistoryServiceConfig{}, nil, pm)

	token := "GHOST:" + testIssuer
	upstreamID := "GHOST-" + testIssuer + "-2"

	got, err := svc.GetTokenStats(context.Background(), token, types.PUBLIC)
	require.NoError(t, err)
	assert.Nil(t, got.SupplyOnStellar)
	assert.Equal(t, 1, expert.CallCount(upstreamID))
	assert.Equal(t, emptySeriesCacheTTL, cache.TTL("tokenstats:v1:public:"+token),
		"not-found meta caches at the same short negative TTL as empty series")

	got, err = svc.GetTokenStats(context.Background(), token, types.PUBLIC)
	require.NoError(t, err)
	assert.Nil(t, got.SupplyOnStellar, "a cached not-found still yields an empty stats block, not an error")
	assert.Equal(t, 1, expert.CallCount(upstreamID),
		"a second request within the negative TTL must make zero upstream asset calls")

	assert.Equal(t, float64(1), testutil.ToFloat64(pm.TokenStatsCacheOutcomes.WithLabelValues(types.PUBLIC, "negative_hit")))
	assert.Equal(t, float64(0), testutil.ToFloat64(pm.TokenStatsCacheOutcomes.WithLabelValues(types.PUBLIC, "hit")))
}

// An UNCLASSIFIED error is not an upstream verdict — it is a decode failure
// or a bug on our side. Those must stay errors: laundering them into an empty
// 200 would report "this token has no stats" for what is actually our defect.
func TestTokenStats_UnclassifiedErrorIsStillAnError(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.SetErr("XLM", errors.New("boom"))
	svc := newStatsService(expert, nil, PriceHistoryServiceConfig{})
	_, err := svc.GetTokenStats(context.Background(), "XLM", types.PUBLIC)
	require.Error(t, err)
}

// A transient upstream failure degrades to the same empty 200 as an unknown
// asset, instead of 500ing. Returning our own 5xx for an upstream blip spent
// our error budget on someone else's outage and paged
// FreighterBackendV2High5xxRate as though Freighter were failing user
// traffic — and 503 would have tripped the same alert. Detection belongs on
// the dependency metric, which is what the StellarExpert* alerts watch.
func TestTokenStats_TransientUpstreamFailureDegradesToEmpty(t *testing.T) {
	t.Parallel()

	for _, code := range []int{500, 502, 503, 504, 429} {
		t.Run(fmt.Sprintf("http_%d", code), func(t *testing.T) {
			t.Parallel()
			expert := newFakeStellarExpert()
			expert.SetErr("XLM", &metrics.UpstreamError{
				Kind: "http_error", Code: code,
				Err: fmt.Errorf("stellar expert asset status %d", code),
			})
			cache := newFakeJSONCache()
			svc := newStatsService(expert, cache, PriceHistoryServiceConfig{})

			got, err := svc.GetTokenStats(context.Background(), "XLM", types.PUBLIC)
			require.NoError(t, err, "an upstream blip must not become our 5xx")
			require.NotNil(t, got)
			assert.Nil(t, got.SupplyOnStellar, "no rows, same as an unknown asset")
			assert.Nil(t, got.Holders)

			// Crucially NOT cached: caching a blip would keep the section
			// empty for the TTL after upstream recovered.
			assert.Equal(t, time.Duration(0), cache.TTL("tokenstats:v1:public:XLM"))
			_, err = svc.GetTokenStats(context.Background(), "XLM", types.PUBLIC)
			require.NoError(t, err)
			assert.Equal(t, 2, expert.CallCount("XLM"), "must retry upstream, not serve a cached empty")
		})
	}
}

// A transport failure (no status code) is still upstream, so it degrades too.
func TestTokenStats_TransportFailureDegradesToEmpty(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert()
	expert.SetErr("XLM", &metrics.UpstreamError{Kind: "http_error", Err: errors.New("dial tcp: connection refused")})
	svc := newStatsService(expert, newFakeJSONCache(), PriceHistoryServiceConfig{})

	got, err := svc.GetTokenStats(context.Background(), "XLM", types.PUBLIC)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Nil(t, got.SupplyOnStellar)
}

// A credential rejection is OUR misconfiguration, fails every asset, and never
// self-heals. It must NOT be absorbed into an empty section — that would hide a
// total outage behind a UI state meaning "this token has no data".
func TestTokenStats_CredentialFailureStaysAnError(t *testing.T) {
	t.Parallel()

	for _, code := range []int{401, 402, 403} {
		t.Run(fmt.Sprintf("http_%d", code), func(t *testing.T) {
			t.Parallel()
			expert := newFakeStellarExpert()
			expert.SetErr("XLM", &metrics.UpstreamError{
				Kind: "http_error", Code: code,
				Err: fmt.Errorf("%w: stellar expert asset status %d", ErrUpstreamAuth, code),
			})
			svc := newStatsService(expert, newFakeJSONCache(), PriceHistoryServiceConfig{})

			_, err := svc.GetTokenStats(context.Background(), "XLM", types.PUBLIC)
			require.Error(t, err, "a bad key must not look like a token with no stats")
			assert.True(t, errors.Is(err, ErrUpstreamAuth))
		})
	}
}

// The asset payload caches under tokenstats:v1 at the configured TTL, and
// the history service reads the SAME entry — one upstream asset call serves
// both endpoints.
func TestTokenStats_SharesCachedAssetPayloadWithHistory(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expert := newFakeStellarExpert()
	expert.Set("XLM", assetWithStats(0.16, "1054439020873472865", nil, int64Ptr(9926520)))
	expert.SetCandles("XLM", historyCandles(now, 24*time.Hour, 900, 0.15, 0.16))
	cache := newFakeJSONCache()

	svc := NewPriceHistoryService(expert, cache, spotPrices("0.16"), PriceHistoryServiceConfig{TokenStatsCacheTTL: 30 * time.Minute}, nil, nil)

	_, err := svc.GetPriceHistory(context.Background(), "XLM", types.PUBLIC, "1D")
	require.NoError(t, err)
	assert.Equal(t, 1, expert.CallCount("XLM"))
	assert.Equal(t, 30*time.Minute, cache.TTL("tokenstats:v1:public:XLM"))

	got, err := svc.GetTokenStats(context.Background(), "XLM", types.PUBLIC)
	require.NoError(t, err)
	require.NotNil(t, got.SupplyOnStellar)
	assert.Equal(t, "105443902087.3472865", *got.SupplyOnStellar, "supply survives the cache round-trip exactly")
	assert.Equal(t, 1, expert.CallCount("XLM"), "stats must reuse the history service's cached asset payload")
}

// `decimals` is contract-controlled: a hostile SEP-41 token can report an
// arbitrarily large value. Scaling must never allocate proportional to it —
// the supply row is simply omitted (unsourceable) and the rest of the stats
// block still serves. No real token exceeds ~18 decimals; Stellar's default
// is 7.
func TestTokenStats_AbsurdDecimalsOmitsSupplyWithoutAllocating(t *testing.T) {
	// NOT parallel: the assertion below reads process-global runtime.MemStats,
	// so any other test allocating between the two snapshots inflates the
	// delta and fails this nondeterministically.

	expert := newFakeStellarExpert()
	hostile := 2_000_000_000 // 2e9: a naive strings.Repeat would allocate ~2 GB
	expert.Set("EVIL-"+testIssuer+"-1", assetWithStats(1.0, "1000000", &hostile, int64Ptr(3)))

	svc := newStatsService(expert, nil, PriceHistoryServiceConfig{})

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	done := make(chan struct{})
	var got *types.TokenStats
	var err error
	go func() {
		defer close(done)
		got, err = svc.GetTokenStats(context.Background(), "EVIL:"+testIssuer, types.PUBLIC)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("GetTokenStats did not return promptly for a hostile decimals value")
	}
	runtime.ReadMemStats(&after)

	require.NoError(t, err)
	assert.Nil(t, got.SupplyOnStellar, "an out-of-range decimals makes supply unsourceable")
	require.NotNil(t, got.Holders, "the rest of the stats block still serves")
	assert.Equal(t, int64(3), *got.Holders)
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(64<<20),
		"scaling must never allocate proportional to the contract-controlled decimals")
}

func TestTokenStats_RejectsUnsupportedNetwork(t *testing.T) {
	t.Parallel()

	svc := newStatsService(newFakeStellarExpert(), nil, PriceHistoryServiceConfig{})
	_, err := svc.GetTokenStats(context.Background(), "XLM", types.FUTURENET)
	require.Error(t, err)
}

func TestScaleSupplyByDecimals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		raw      string
		decimals int
		want     string
		ok       bool
	}{
		{"XLM 19-digit supply, 7 decimals", "1054439020873472865", 7, "105443902087.3472865", true},
		{"trailing zeros trimmed", "1000000000", 7, "100", true},
		{"fraction only", "1", 7, "0.0000001", true},
		{"exactly decimals digits", "1234567", 7, "0.1234567", true},
		{"zero decimals", "42", 0, "42", true},
		{"zero supply", "0", 7, "0", true},
		{"18-decimal token", "5000000000000000000", 18, "5", true},
		{"empty (absent upstream)", "", 7, "", false},
		{"non-integer rejected", "10.5", 7, "", false},
		{"exponent rejected", "1e18", 7, "", false},
		{"negative decimals rejected", "100", -1, "", false},
		{"max supported decimals accepted", "1", maxSupplyDecimals, "0." + strings.Repeat("0", maxSupplyDecimals-1) + "1", true},
		{"absurd decimals rejected", "100", maxSupplyDecimals + 1, "", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := scaleSupplyByDecimals(tc.raw, tc.decimals)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The other half of why tokenstats ships at v1 rather than rotating: an entry
// written before cachedAssetMeta gained `notFound` must still read back as a
// HIT with every field it carries intact. That holds because `notFound` was
// only ever written for an ABSENT asset, so an old entry is by construction a
// found one and the zero value false is the right answer — and because the
// surviving fields never changed name or type on this branch. The `price` and
// `created` such an entry also carries are simply ignored.
//
// This is the guard for a future field whose zero value is NOT the right
// answer for an old entry: add one and this test must be what fails.
func TestTokenStats_PreNotFoundCacheEntryReadsAsAHit(t *testing.T) {
	t.Parallel()

	expert := newFakeStellarExpert() // unknown → any upstream call would 404
	cache := newFakeJSONCache()
	key := tokenStatsCacheKey("public", "USDC:"+testIssuer)

	// The original shape, written by an earlier commit of this branch: the
	// now-dropped price/created are present, notFound is absent entirely.
	require.NoError(t, cache.SetJSON(context.Background(), key, map[string]any{
		"price":    0.9998,
		"supply":   "1054439020873472865",
		"decimals": 7,
		"volume7d": float64(xlmRawVolume7d),
		"created":  1611161688,
		"funded":   9926520,
	}, time.Hour))

	svc := newStatsService(expert, cache, PriceHistoryServiceConfig{})
	stats, err := svc.GetTokenStats(context.Background(), "USDC:"+testIssuer, types.PUBLIC)
	require.NoError(t, err, "an old entry is a found asset, not a cached not-found")
	assert.Equal(t, 0, expert.CallCount("USDC-"+testIssuer+"-1"),
		"the old shape is readable, so it must be served from cache without an upstream call")

	require.NotNil(t, stats.SupplyOnStellar, "supply must survive the shape change")
	assert.Equal(t, "105443902087.3472865", *stats.SupplyOnStellar)
	require.NotNil(t, stats.Holders)
	assert.Equal(t, int64(9926520), *stats.Holders)
}

// The stats-side half of the same guarantee. Both the not-found case and the
// UpstreamError default arm return an empty 200, so the RESPONSE cannot tell
// them apart — asserting only on the return value would pass even with the
// sentinel match broken. The log line is what separates them: the degraded
// arm warns "upstream unavailable", and an authoritative 404 must not, or
// every unpriced SEP-41 token spams a warning reserved for real outages.
//
// Not parallel: it swaps the process-global logger.
func TestTokenStats_WrappedNotFoundStillYieldsEmptyStats(t *testing.T) {
	var logs bytes.Buffer
	logger.SetOutput(&logs)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })

	expert := newFakeStellarExpert()
	expert.SetErr("BOGUS-"+testIssuer+"-2", &metrics.UpstreamError{
		Kind: "http_error", Code: 404,
		Err: fmt.Errorf("%w: stellar expert asset status 404", ErrAssetNotFound),
	})

	svc := newStatsService(expert, newFakeJSONCache(), PriceHistoryServiceConfig{})
	stats, err := svc.GetTokenStats(context.Background(), "BOGUS:"+testIssuer, types.PUBLIC)
	require.NoError(t, err, "an unknown asset is an empty 200, never an error")
	require.NotNil(t, stats)
	assert.Nil(t, stats.SupplyOnStellar)
	assert.Nil(t, stats.Holders)

	assert.NotContains(t, logs.String(), "upstream unavailable",
		"a wrapped 404 must match the not-found case, not fall through to the degraded-upstream arm")
}

// The negative marker must never outlive the positive payloads sharing its
// keyspace. Both services write tokenstats:v1, and an operator who shortens
// --token-stats-cache-ttl-seconds during an upstream incident is doing it to
// drain poisoned state — a marker pinned at a flat 15m would keep serving the
// bad answer for 15m after positive entries had already refreshed, making the
// lever inert for the case it exists for.
func TestTokenStats_NotFoundMarkerCapsAtTheConfiguredStatsTTL(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		statsTTL time.Duration
		want     time.Duration
	}{
		{"operator shortens below the flat 15m", 5 * time.Minute, 5 * time.Minute},
		{"default 1h is capped to 15m", time.Hour, emptySeriesCacheTTL},
		{"unset falls back to the cap", 0, emptySeriesCacheTTL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expert := newFakeStellarExpert() // unknown asset → ErrAssetNotFound
			cache := newFakeJSONCache()
			svc := newStatsService(expert, cache, PriceHistoryServiceConfig{TokenStatsCacheTTL: tc.statsTTL})

			_, err := svc.GetTokenStats(context.Background(), "BOGUS:"+testIssuer, types.PUBLIC)
			require.NoError(t, err, "an unknown asset is an empty 200")

			key := tokenStatsCacheKey("public", "BOGUS:"+testIssuer)
			assert.Equal(t, tc.want, cache.TTL(key),
				"the not-found marker must cap at the configured stats TTL, never exceed it")
		})
	}
}
