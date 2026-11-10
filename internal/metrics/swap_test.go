// ABOUTME: Unit tests for the swap routing Prometheus metrics.
// ABOUTME: Verifies the counters and histogram record the expected labels and that the nil-safe helpers are no-ops.
package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestSwapMetrics(t *testing.T) {
	t.Parallel()
	m := NewSwap(prometheus.NewRegistry())
	RecordSwapSource(m, "lifi", "quote", "no_route")
	RecordSwapUnknownHop(m, "Phoenix")
	RecordSwapUnknownHop(m, "SomethingNew")
	RecordSwapWinner(m, "classic", "classic", 54.2)

	assert.Equal(t, 1.0, testutil.ToFloat64(m.SourceRequests.WithLabelValues("lifi", "quote", "no_route")))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.UnknownHops.WithLabelValues("Phoenix")))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.UnknownHops.WithLabelValues("other")))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.Winners.WithLabelValues("classic", "classic")))

	// nil-safe
	RecordSwapSource(nil, "lifi", "quote", "ok")
	RecordSwapUnknownHop(nil, "Aqua")
	RecordSwapWinner(nil, "classic", "classic", 1)
}
