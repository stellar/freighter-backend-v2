package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	testAccountAddress  = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	testContractAddress = "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"
	testMaxLimit        = 100
)

// The canonical form is both what we send upstream and the cache key, so two
// queries StellarExpert answers identically must canonicalize identically,
// and nothing may change what StellarExpert would answer.
func TestCanonicalAssetSearchParams(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   url.Values
		want url.Values
	}{
		{
			name: "trending parameters pass through",
			in:   url.Values{"sort": {"volume7d"}, "order": {"desc"}, "limit": {"50"}},
			want: url.Values{"sort": {"volume7d"}, "order": {"desc"}, "limit": {"50"}},
		},
		{
			name: "paging parameters pass through",
			in:   url.Values{"sort": {"created"}, "cursor": {"8812"}, "includeUninitialized": {"true"}},
			want: url.Values{"sort": {"created"}, "cursor": {"8812"}, "includeUninitialized": {"true"}},
		},
		{
			// StellarExpert ignores case for codes and names (measured on
			// Testnet: usdc, USDC and UsDc return identical bodies).
			name: "search text is trimmed and lowercased",
			in:   url.Values{"search": {"  UsDc "}},
			want: url.Values{"search": {"usdc"}},
		},
		{
			// ...but matches full addresses case-sensitively: uppercase
			// returned 14 records, lowercase 0. Uppercase is canonical.
			name: "a full account address is uppercased",
			in:   url.Values{"search": {strings.ToLower(testAccountAddress)}},
			want: url.Values{"search": {testAccountAddress}},
		},
		{
			name: "a full contract address is uppercased",
			in:   url.Values{"search": {" " + strings.ToLower(testContractAddress) + " "}},
			want: url.Values{"search": {testContractAddress}},
		},
		{
			// A partial address matched nothing in either case upstream, so
			// it is ordinary text.
			name: "a partial address is plain text",
			in:   url.Values{"search": {"GA5ZSEJYB37J"}},
			want: url.Values{"search": {"ga5zsejyb37j"}},
		},
		{
			name: "a 100-character search is accepted",
			in:   url.Values{"search": {strings.Repeat("a", 100)}},
			want: url.Values{"search": {strings.Repeat("a", 100)}},
		},
		{
			name: "empty values are dropped",
			in:   url.Values{"search": {"   "}, "sort": {""}, "limit": {"50"}},
			want: url.Values{"limit": {"50"}},
		},
		{
			name: "limit is written canonically",
			in:   url.Values{"limit": {" 050 "}},
			want: url.Values{"limit": {"50"}},
		},
		{
			name: "limit at the cap is kept",
			in:   url.Values{"limit": {"100"}},
			want: url.Values{"limit": {"100"}},
		},
		{
			// Clamped the way StellarExpert clamps past a plan's batch size:
			// the caller gets a smaller page, not an error.
			name: "limit above the cap is clamped to it",
			in:   url.Values{"limit": {"10000"}},
			want: url.Values{"limit": {"100"}},
		},
		{
			name: "no parameters",
			in:   url.Values{},
			want: url.Values{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := canonicalAssetSearchParams(tc.in, testMaxLimit)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCanonicalAssetSearchParams_Rejects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   url.Values
	}{
		// A typo would otherwise quietly return the unfiltered list.
		{"an unknown parameter", url.Values{"q": {"usdc"}}},
		// Present with any value, estimate makes upstream answer a price quote
		// instead of records.
		{"estimate", url.Values{"search": {"usdc"}, "estimate": {""}}},
		{"a repeated parameter", url.Values{"search": {"usdc", "xlm"}}},
		{"a search over 100 characters", url.Values{"search": {strings.Repeat("a", 101)}}},
		{"any value over 100 characters", url.Values{"cursor": {strings.Repeat("9", 101)}}},
		{"a non-numeric limit", url.Values{"limit": {"ten"}}},
		{"a zero limit", url.Values{"limit": {"0"}}},
		{"a negative limit", url.Values{"limit": {"-5"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := canonicalAssetSearchParams(tc.in, testMaxLimit)
			require.ErrorIs(t, err, ErrInvalidAssetSearchParams)
			assert.Nil(t, got)
		})
	}
}

// searchBody is a StellarExpert /asset response in its real shape.
const searchBody = `{"_links":{"self":{"href":"/explorer/public/asset?search=usdc&order=desc&limit=50&cursor=0"}},"_embedded":{"records":[{"asset":"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN-1","domain":"centre.io","price":1.0001,"volume7d":123456789,"tomlInfo":{"code":"USDC","issuer":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}}]}}`

// listCall is one ListAssets call the fake upstream received.
type listCall struct {
	network string
	query   url.Values
}

// newListingExpert returns a fake StellarExpert whose ListAssets records every
// call and answers with respond, which gets the 1-based call number.
func newListingExpert(respond func(call int) (json.RawMessage, error)) (*fakeStellarExpert, func() []listCall) {
	fake := newFakeStellarExpert()
	var mu sync.Mutex
	var calls []listCall
	fake.listAssets = func(_ context.Context, network string, query url.Values) (json.RawMessage, error) {
		mu.Lock()
		calls = append(calls, listCall{network: network, query: query})
		n := len(calls)
		mu.Unlock()
		return respond(n)
	}
	return fake, func() []listCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]listCall(nil), calls...)
	}
}

func alwaysBody(int) (json.RawMessage, error) { return json.RawMessage(searchBody), nil }

// onlyTTL returns the TTL of the cache's single entry.
func onlyTTL(t *testing.T, cache *fakeJSONCache) time.Duration {
	t.Helper()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Len(t, cache.ttls, 1)
	for _, ttl := range cache.ttls {
		return ttl
	}
	return 0
}

func cacheLen(cache *fakeJSONCache) int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return len(cache.entries)
}

func TestAssetSearch_MissSendsTheCanonicalQueryAndCachesTheBody(t *testing.T) {
	t.Parallel()

	fake, calls := newListingExpert(alwaysBody)
	cache := newFakeJSONCache()
	svc := NewAssetSearchService(fake, cache, AssetSearchServiceConfig{CacheTTL: 5 * time.Hour, MaxLimit: testMaxLimit}, nil)

	got, err := svc.SearchAssets(context.Background(), types.PUBLIC, url.Values{"search": {" USDC "}, "limit": {"500"}})
	require.NoError(t, err)
	assert.JSONEq(t, searchBody, string(got))
	assert.Equal(t, []listCall{{network: types.PUBLIC, query: url.Values{"search": {"usdc"}, "limit": {"100"}}}}, calls(),
		"upstream gets the canonical query: trimmed, lowercased, limit clamped")
	assert.Equal(t, 5*time.Hour, onlyTTL(t, cache), "cached for the configured TTL")
}

// Zero config must not mean "no cache" or "no cap" against a paid upstream.
func TestAssetSearch_ZeroConfigFallsBackToSafeDefaults(t *testing.T) {
	t.Parallel()

	fake, calls := newListingExpert(alwaysBody)
	cache := newFakeJSONCache()
	svc := NewAssetSearchService(fake, cache, AssetSearchServiceConfig{}, nil)

	_, err := svc.SearchAssets(context.Background(), types.PUBLIC, url.Values{"limit": {"10000"}})
	require.NoError(t, err)
	require.Len(t, calls(), 1)
	assert.Equal(t, url.Values{"limit": {"100"}}, calls()[0].query, "the limit cap defaults to 100")
	assert.Equal(t, 6*time.Hour, onlyTTL(t, cache), "the cache TTL defaults to 6h")
}

func TestAssetSearch_EquivalentQueriesShareOneUpstreamCall(t *testing.T) {
	t.Parallel()

	fake, calls := newListingExpert(alwaysBody)
	svc := NewAssetSearchService(fake, newFakeJSONCache(), AssetSearchServiceConfig{MaxLimit: testMaxLimit}, nil)

	_, err := svc.SearchAssets(context.Background(), types.PUBLIC, url.Values{"search": {"usdc"}, "limit": {"50"}})
	require.NoError(t, err)
	got, err := svc.SearchAssets(context.Background(), types.PUBLIC, url.Values{"limit": {"050"}, "search": {"USDC"}})
	require.NoError(t, err)

	assert.JSONEq(t, searchBody, string(got))
	assert.Len(t, calls(), 1, "the second query asks the same question and must be answered from cache")
}

func TestAssetSearch_NetworksDoNotShareCacheEntries(t *testing.T) {
	t.Parallel()

	fake, calls := newListingExpert(alwaysBody)
	svc := NewAssetSearchService(fake, newFakeJSONCache(), AssetSearchServiceConfig{MaxLimit: testMaxLimit}, nil)

	q := url.Values{"search": {"usdc"}}
	_, err := svc.SearchAssets(context.Background(), types.PUBLIC, q)
	require.NoError(t, err)
	_, err = svc.SearchAssets(context.Background(), types.TESTNET, q)
	require.NoError(t, err)

	got := calls()
	require.Len(t, got, 2)
	assert.Equal(t, types.PUBLIC, got[0].network)
	assert.Equal(t, types.TESTNET, got[1].network)
}

// A failure is never cached: a rejected key (or an empty balance) must be
// retried on the next request, not replayed from cache for hours.
func TestAssetSearch_UpstreamErrorsAreNotCached(t *testing.T) {
	t.Parallel()

	creds := &metrics.UpstreamError{
		Kind: "http_error", Code: http.StatusPaymentRequired,
		Err: fmt.Errorf("%w: stellar expert asset list status 402", ErrUpstreamAuth),
	}
	fake, calls := newListingExpert(func(call int) (json.RawMessage, error) {
		if call == 1 {
			return nil, creds
		}
		return json.RawMessage(searchBody), nil
	})
	cache := newFakeJSONCache()
	svc := NewAssetSearchService(fake, cache, AssetSearchServiceConfig{MaxLimit: testMaxLimit}, nil)
	q := url.Values{"search": {"usdc"}}

	_, err := svc.SearchAssets(context.Background(), types.PUBLIC, q)
	require.ErrorIs(t, err, ErrUpstreamAuth, "the credential sentinel must survive so the handler stays loud")
	assert.Equal(t, 0, cacheLen(cache))

	got, err := svc.SearchAssets(context.Background(), types.PUBLIC, q)
	require.NoError(t, err)
	assert.JSONEq(t, searchBody, string(got))
	assert.Len(t, calls(), 2)
}

// No cache here, so only request coalescing can hold this to one paid call.
func TestAssetSearch_ConcurrentMissesShareOneUpstreamCall(t *testing.T) {
	t.Parallel()

	fake, calls := newListingExpert(func(int) (json.RawMessage, error) {
		// Wide enough that every caller is waiting on the shared fetch
		// before it completes.
		time.Sleep(50 * time.Millisecond)
		return json.RawMessage(searchBody), nil
	})
	svc := NewAssetSearchService(fake, nil, AssetSearchServiceConfig{MaxLimit: testMaxLimit}, nil)

	const n = 10
	var wg sync.WaitGroup
	bodies := make([]json.RawMessage, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bodies[i], errs[i] = svc.SearchAssets(context.Background(), types.PUBLIC, url.Values{"search": {"usdc"}})
		}(i)
	}
	wg.Wait()

	assert.Len(t, calls(), 1, "concurrent misses for one query must share one upstream call")
	for i := range bodies {
		require.NoError(t, errs[i])
		assert.JSONEq(t, searchBody, string(bodies[i]))
	}
}

// A caller who gives up gets its own context error, but the paid fetch still
// finishes and lands in the cache for the next caller.
func TestAssetSearch_CallerLeavingDoesNotWasteTheFetch(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	fake, calls := newListingExpert(func(int) (json.RawMessage, error) {
		entered <- struct{}{}
		<-release
		return json.RawMessage(searchBody), nil
	})
	cache := newFakeJSONCache()
	svc := NewAssetSearchService(fake, cache, AssetSearchServiceConfig{MaxLimit: testMaxLimit}, nil)
	q := url.Values{"search": {"usdc"}}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := svc.SearchAssets(ctx, types.PUBLIC, q)
		errCh <- err
	}()
	<-entered
	cancel()
	require.ErrorIs(t, <-errCh, context.Canceled)

	close(release)
	require.Eventually(t, func() bool { return cacheLen(cache) == 1 }, time.Second, 5*time.Millisecond,
		"the abandoned fetch must still be cached")

	got, err := svc.SearchAssets(context.Background(), types.PUBLIC, q)
	require.NoError(t, err)
	assert.JSONEq(t, searchBody, string(got))
	assert.Len(t, calls(), 1)
}

func TestAssetSearch_InvalidParamsNeverReachUpstream(t *testing.T) {
	t.Parallel()

	fake, calls := newListingExpert(alwaysBody)
	svc := NewAssetSearchService(fake, newFakeJSONCache(), AssetSearchServiceConfig{MaxLimit: testMaxLimit}, nil)

	_, err := svc.SearchAssets(context.Background(), types.PUBLIC, url.Values{"q": {"usdc"}})
	require.ErrorIs(t, err, ErrInvalidAssetSearchParams)
	assert.Empty(t, calls())
}

func TestAssetSearch_RejectsUnsupportedNetwork(t *testing.T) {
	t.Parallel()

	fake, calls := newListingExpert(alwaysBody)
	svc := NewAssetSearchService(fake, newFakeJSONCache(), AssetSearchServiceConfig{MaxLimit: testMaxLimit}, nil)

	_, err := svc.SearchAssets(context.Background(), types.FUTURENET, url.Values{"search": {"usdc"}})
	require.Error(t, err)
	assert.Empty(t, calls())
}

func TestAssetSearch_WorksWithoutACache(t *testing.T) {
	t.Parallel()

	fake, calls := newListingExpert(alwaysBody)
	svc := NewAssetSearchService(fake, nil, AssetSearchServiceConfig{MaxLimit: testMaxLimit}, nil)

	for i := 0; i < 2; i++ {
		got, err := svc.SearchAssets(context.Background(), types.PUBLIC, url.Values{"search": {"usdc"}})
		require.NoError(t, err)
		assert.JSONEq(t, searchBody, string(got))
	}
	assert.Len(t, calls(), 2, "with no cache every request goes upstream")
}

// Redis being down costs the cache, not the answer.
func TestAssetSearch_CacheReadFailureFallsThroughToUpstream(t *testing.T) {
	t.Parallel()

	fake, calls := newListingExpert(alwaysBody)
	svc := NewAssetSearchService(fake, &errCache{mgetErr: errors.New("connection refused")}, AssetSearchServiceConfig{MaxLimit: testMaxLimit}, nil)

	got, err := svc.SearchAssets(context.Background(), types.PUBLIC, url.Values{"search": {"usdc"}})
	require.NoError(t, err)
	assert.JSONEq(t, searchBody, string(got))
	assert.Len(t, calls(), 1)
}

func TestAssetSearch_CountsCacheOutcomes(t *testing.T) {
	t.Parallel()

	fake, _ := newListingExpert(alwaysBody)
	m := metrics.NewPrices(prometheus.NewRegistry())
	svc := NewAssetSearchService(fake, newFakeJSONCache(), AssetSearchServiceConfig{MaxLimit: testMaxLimit}, m)

	q := url.Values{"search": {"usdc"}}
	_, err := svc.SearchAssets(context.Background(), types.PUBLIC, q)
	require.NoError(t, err)
	_, err = svc.SearchAssets(context.Background(), types.PUBLIC, q)
	require.NoError(t, err)

	assert.Equal(t, float64(1), testutil.ToFloat64(m.AssetSearchCacheOutcomes.WithLabelValues(types.PUBLIC, "miss")))
	assert.Equal(t, float64(1), testutil.ToFloat64(m.AssetSearchCacheOutcomes.WithLabelValues(types.PUBLIC, "hit")))
}
