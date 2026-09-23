// ABOUTME: Unit tests for Prometheus metric definitions, registration, and the Record helper.
// ABOUTME: Verifies metrics register without panic, pass lint, Record records correctly, and ClassifyError works.
package metrics

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMetrics_RegistersWithoutPanic(t *testing.T) {
	reg := prometheus.NewRegistry()
	require.NotPanics(t, func() {
		m := NewMetrics(reg)
		require.NotNil(t, m)
		require.NotNil(t, m.HTTP)
		require.NotNil(t, m.Service)
		require.NotNil(t, m.Prices)
	})
}

func TestNewMetrics_DoubleRegistrationPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	NewMetrics(reg)
	require.Panics(t, func() {
		NewMetrics(reg)
	})
}

func TestNewHTTP_LintPasses(t *testing.T) {
	reg := prometheus.NewRegistry()
	NewHTTP(reg)

	problems, err := testutil.GatherAndLint(reg)
	require.NoError(t, err)
	assert.Empty(t, problems, "lint problems: %v", problems)
}

func TestNewService_LintPasses(t *testing.T) {
	reg := prometheus.NewRegistry()
	NewService(reg)

	problems, err := testutil.GatherAndLint(reg)
	require.NoError(t, err)
	assert.Empty(t, problems, "lint problems: %v", problems)
}

func TestNewPrices_LintPasses(t *testing.T) {
	reg := prometheus.NewRegistry()
	NewPrices(reg)

	problems, err := testutil.GatherAndLint(reg)
	require.NoError(t, err)
	assert.Empty(t, problems, "lint problems: %v", problems)
}

func TestNewHTTP_MetricCount(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := NewHTTP(reg)

	// Emit one observation to ensure all metric families appear
	h.RequestsTotal.WithLabelValues("test", "GET", "200").Inc()
	h.RequestDuration.WithLabelValues("test", "GET", "200").Observe(0.1)
	h.InFlightRequests.Inc()

	// 3 metric families: requests_total, request_duration_seconds, in_flight_requests
	count := testutil.CollectAndCount(reg)
	assert.Equal(t, 3, count)
}

func TestNewService_MetricCount(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := NewService(reg)

	// Emit one observation to ensure all metric families appear
	s.CallsTotal.WithLabelValues("test", "test", "test").Inc()
	s.CallDuration.WithLabelValues("test", "test", "test").Observe(0.1)
	s.ErrorsTotal.WithLabelValues("test", "test", "test", "other").Inc()

	// 3 metric families: calls_total, call_duration_seconds, errors_total
	count := testutil.CollectAndCount(reg)
	assert.Equal(t, 3, count)
}

func TestRecord_IncrementsCallsAndDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	svc := NewService(reg)

	Record(svc, "rpc", "GetHealth", "TESTNET", 0.5, nil)

	callCount := testutil.ToFloat64(svc.CallsTotal.WithLabelValues("rpc", "GetHealth", "TESTNET"))
	assert.Equal(t, float64(1), callCount)

	// Verify no errors recorded on success
	errCount := testutil.ToFloat64(svc.ErrorsTotal.WithLabelValues("rpc", "GetHealth", "TESTNET", "internal"))
	assert.Equal(t, float64(0), errCount)
}

func TestRecord_IncrementsErrorsOnFailure(t *testing.T) {
	reg := prometheus.NewRegistry()
	svc := NewService(reg)

	Record(svc, "rpc", "GetHealth", "PUBLIC", 0.1, fmt.Errorf("something broke"))

	callCount := testutil.ToFloat64(svc.CallsTotal.WithLabelValues("rpc", "GetHealth", "PUBLIC"))
	assert.Equal(t, float64(1), callCount)

	errCount := testutil.ToFloat64(svc.ErrorsTotal.WithLabelValues("rpc", "GetHealth", "PUBLIC", "internal"))
	assert.Equal(t, float64(1), errCount)
}

func TestRecord_NilServiceDoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		Record(nil, "rpc", "GetHealth", "TESTNET", 0.1, nil)
	})
}

func TestSanitizeClient(t *testing.T) {
	cases := map[string]string{
		"freighter-extension": "freighter-extension",
		"freighter-mobile":    "freighter-mobile",
		"":                    "other",
		"freighter-cli":       "other",
		"attacker-supplied-🦄": "other",
	}
	for in, want := range cases {
		assert.Equal(t, want, SanitizeClient(in), "input %q", in)
	}
}

func TestUpstreamError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *UpstreamError
		expected string
	}{
		{
			name:     "kind only",
			err:      &UpstreamError{Kind: "simulation_error", Err: fmt.Errorf("sim failed")},
			expected: "simulation_error: sim failed",
		},
		{
			name:     "kind with code",
			err:      &UpstreamError{Kind: "http_error", Code: 503, Err: fmt.Errorf("service unavailable")},
			expected: "http_error (code 503): service unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

func TestUpstreamError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("inner error")
	upErr := &UpstreamError{Kind: "graphql_error", Err: inner}
	assert.ErrorIs(t, upErr, inner)
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		// timeout: context deadline/canceled
		{
			name:     "deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: "timeout",
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: "timeout",
		},
		{
			name:     "wrapped deadline exceeded",
			err:      fmt.Errorf("call failed: %w", context.DeadlineExceeded),
			expected: "timeout",
		},
		{
			name:     "wrapped context canceled",
			err:      fmt.Errorf("call failed: %w", context.Canceled),
			expected: "timeout",
		},
		// connection: network-level failures
		{
			name: "net.OpError",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: fmt.Errorf("connection refused"),
			},
			expected: "connection",
		},
		{
			name: "wrapped net.OpError",
			err: fmt.Errorf("rpc failed: %w", &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: fmt.Errorf("connection refused"),
			}),
			expected: "connection",
		},
		{
			name: "url.Error",
			err: &url.Error{
				Op:  "Post",
				URL: "http://localhost:8000",
				Err: fmt.Errorf("connection refused"),
			},
			expected: "connection",
		},
		// UpstreamError: simulation, graphql, http errors
		{
			name:     "simulation error",
			err:      &UpstreamError{Kind: "simulation_error", Err: fmt.Errorf("simulateTransaction returned error: foo")},
			expected: "simulation_error",
		},
		{
			name:     "graphql error",
			err:      &UpstreamError{Kind: "graphql_error", Err: fmt.Errorf("GraphQL error: something")},
			expected: "graphql_error",
		},
		{
			name:     "http error with code 503",
			err:      &UpstreamError{Kind: "http_error", Code: 503, Err: fmt.Errorf("health endpoint returned status 503")},
			expected: "http_error:503",
		},
		{
			name:     "http error with code 429",
			err:      &UpstreamError{Kind: "http_error", Code: 429, Err: fmt.Errorf("rate limited")},
			expected: "http_error:429",
		},
		{
			name:     "http error without code",
			err:      &UpstreamError{Kind: "http_error", Err: fmt.Errorf("unexpected statusCode=500")},
			expected: "http_error",
		},
		{
			name:     "wrapped UpstreamError preserves kind",
			err:      fmt.Errorf("call failed: %w", &UpstreamError{Kind: "simulation_error", Err: fmt.Errorf("error")}),
			expected: "simulation_error",
		},
		// Priority: timeout wins over UpstreamError
		{
			name:     "UpstreamError wrapping deadline exceeded classifies as timeout",
			err:      &UpstreamError{Kind: "http_error", Code: 504, Err: context.DeadlineExceeded},
			expected: "timeout",
		},
		// Priority: connection wins over UpstreamError
		{
			name: "UpstreamError wrapping net.OpError classifies as connection",
			err: &UpstreamError{Kind: "http_error", Code: 503, Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: fmt.Errorf("connection refused"),
			}},
			expected: "connection",
		},
		// rpc_error: JSON-RPC errors with error code
		{
			name:     "jrpc2 internal error",
			err:      jrpc2.Errorf(jrpc2.InternalError, "server error"),
			expected: "rpc_error:-32603",
		},
		{
			name:     "wrapped jrpc2 method not found",
			err:      fmt.Errorf("rpc call failed: %w", jrpc2.Errorf(jrpc2.MethodNotFound, "not found")),
			expected: "rpc_error:-32601",
		},
		// internal: encoding, validation, and other local failures
		{
			name:     "plain error maps to internal",
			err:      fmt.Errorf("failed to decode XDR"),
			expected: "internal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ClassifyError(tt.err))
		})
	}
}

// Pins the wire names of the price-history/token-stats metric families (B.4)
// — runbooks and alerts reference these strings.
func TestNewPrices_PriceHistoryMetricNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	p := NewPrices(reg)

	p.HistoryCacheOutcomes.WithLabelValues("PUBLIC", "1D", "miss").Inc()
	p.TokenStatsCacheOutcomes.WithLabelValues("PUBLIC", "hit").Inc()
	p.VolumeVerdictNull.WithLabelValues("PUBLIC").Inc()
	p.SkippedTokens.WithLabelValues("PUBLIC").Inc()

	families, err := reg.Gather()
	require.NoError(t, err)
	names := make(map[string]bool, len(families))
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, want := range []string{
		"freighter_price_history_cache_outcomes_total",
		"freighter_token_stats_cache_outcomes_total",
		"freighter_price_history_volume_verdict_null_total",
		"freighter_prices_skipped_tokens_total",
	} {
		assert.True(t, names[want], "expected metric family %s to be registered", want)
	}
}

// The Help strings are the operator-facing contract — they render on /metrics
// and in Grafana tooltips, where the Go doc comments above the fields are not
// visible. They have drifted repeatedly: the field comments were corrected and
// the Help strings were not; a sweep then fixed four series and missed three;
// and successive versions of THIS test pinned needles the pre-fix strings
// already satisfied, so they protected nothing.
//
// Hence the shape below. Descriptors come from the REGISTRY and from the
// struct, and the two sets must match — which pins three things at once: every
// series is registered (a forgotten MustRegister is otherwise invisible, and
// makes the series silently absent from /metrics), every series is pinned, and
// no pin names a series that no longer exists.
//
// Needle style: a leading slash is load-bearing wherever the pre-fix text used
// the bare route name. "the token-stats path" contains "token-stats", so only
// "/token-stats" discriminates. Do not normalise the slashes away.
func TestPrices_HelpStringsNameEveryDrivingEndpoint(t *testing.T) {
	t.Parallel()

	// must: every route that drives the series, so trimming to the most
	// recently added driver fails. mustNot: for single-route series, the
	// neighbour it would plausibly be broadened to name.
	type pin struct{ must, mustNot []string }
	pins := map[string]pin{
		// Written by the prices service; /token-price-history resolves its
		// spot anchor through it, so both drive these two.
		"freighter_prices_cache_outcomes_total":        {must: []string{"/token-prices", "/token-price-history"}},
		"freighter_prices_miss_budget_exhausted_total": {must: []string{"/token-prices", "/token-price-history"}},
		// Incremented directly by the prices service and by the price-history
		// service, which serves two routes of its own.
		"freighter_prices_redis_errors_total": {must: []string{"/token-prices", "/token-price-history", "/token-stats"}},
		// getAssetMeta resolves the asset payload on EVERY history request.
		"freighter_token_stats_cache_outcomes_total": {must: []string{"/token-stats", "/token-price-history"}},
		// Single-route. The mustNot is the neighbour each would wrongly gain:
		// all three share a service or a request with /token-stats.
		"freighter_prices_skipped_tokens_total":        {must: []string{"/token-prices"}, mustNot: []string{"token-price-history"}},
		"freighter_price_history_cache_outcomes_total": {must: []string{"token-price-history"}, mustNot: []string{"token-stats"}},
		// Keyed on the exclusion list, not the phrasing: deleting that
		// sentence is the regression, and a needle on the headline wording
		// would survive it.
		"freighter_price_history_volume_verdict_null_total": {
			must:    []string{"token-price-history", "volume7d", "cancelled"},
			mustNot: []string{"token-stats"},
		},
	}

	reg := prometheus.NewRegistry()
	p := NewPrices(reg)

	// Registered set — catches a field built but never handed to MustRegister,
	// which would otherwise be absent from /metrics with nothing to say so.
	registered := describeHelp(t, reg)

	// Declared set — read straight off the struct, so a collector describes
	// itself whether or not it has observations. No touch list to drift.
	declared := map[string]string{}
	v := reflect.ValueOf(*p)
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		require.True(t, f.IsExported(),
			"unexported field %s: reflection cannot read it — teach this test about it", f.Name)
		c, ok := v.Field(i).Interface().(prometheus.Collector)
		require.True(t, ok, "%s is not a prometheus.Collector — teach this test about it", f.Name)
		for k, h := range describeHelp(t, c) {
			declared[k] = h
		}
	}

	assert.Equal(t, keysOf(declared), keysOf(registered),
		"every series Prices declares must also be registered, or it never reaches /metrics")
	assert.Equal(t, keysOf(pins), keysOf(declared),
		"every series Prices declares needs a pin here, and every pin needs a series")

	for name, want := range pins {
		h, ok := declared[name]
		require.True(t, ok, name)
		for _, d := range want.must {
			assert.Contains(t, h, d, "%s: Help must name %q — an operator reading /metrics has only this", name, d)
		}
		for _, d := range want.mustNot {
			assert.NotContains(t, h, d, "%s: Help must NOT name %q; this series is not driven by it", name, d)
		}
	}
}

// describeHelp maps fqName to Help for everything c describes. Desc exposes no
// accessors, so this parses String() — and UNQUOTES the result, because it is
// %q-formatted and a needle containing a quote would otherwise never match.
func describeHelp(t *testing.T, c prometheus.Collector) map[string]string {
	t.Helper()
	ch := make(chan *prometheus.Desc)
	go func() {
		c.Describe(ch)
		close(ch)
	}()
	out := map[string]string{}
	for d := range ch {
		m := descRE.FindStringSubmatch(d.String())
		require.Len(t, m, 3, "could not parse descriptor: %s", d)
		name, err := strconv.Unquote(`"` + m[1] + `"`)
		require.NoError(t, err)
		help, err := strconv.Unquote(`"` + m[2] + `"`)
		require.NoError(t, err)
		out[name] = help
	}
	return out
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// descRE pulls fqName and help out of Desc.String(), which has no accessors.
// Both are %q-formatted, so the captures are escape-aware and the caller
// unquotes them.
var descRE = regexp.MustCompile(`fqName: "((?:[^"\\]|\\.)*)", help: "((?:[^"\\]|\\.)*)"`)
