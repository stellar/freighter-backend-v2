// ABOUTME: Unit tests for Prometheus metric definitions and registration.
// ABOUTME: Verifies metrics register without panic, pass lint, and classifyError works correctly.
package metrics

import (
	"context"
	"fmt"
	"testing"

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

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
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
		{
			name:     "other error",
			err:      fmt.Errorf("connection refused"),
			expected: "other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ClassifyError(tt.err))
		})
	}
}
