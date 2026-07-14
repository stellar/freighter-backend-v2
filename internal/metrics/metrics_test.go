// ABOUTME: Unit tests for Prometheus metric definitions, registration, and the Record helper.
// ABOUTME: Verifies metrics register without panic, pass lint, Record records correctly, and ClassifyError works.
package metrics

import (
	"context"
	"fmt"
	"net"
	"net/url"
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
