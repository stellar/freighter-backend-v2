// ABOUTME: Defines the swap routing Prometheus collectors and their nil-safe record helpers.
// ABOUTME: Covers per-source call outcomes, unreadable LI.FI hops, and quote winner margins.
package metrics

import "github.com/prometheus/client_golang/prometheus"

// knownHopVenues is the closed set of venue label values. Phoenix and Comet are
// listed although the parser does not read them yet, so an unparsed hop is
// counted under its real name rather than "other".
var knownHopVenues = map[string]bool{"Aqua": true, "Sushi": true, "Soroswap": true, "Phoenix": true, "Comet": true}

// Swap holds swap routing metrics. Must be created via NewSwap.
type Swap struct {
	SourceRequests *prometheus.CounterVec
	UnknownHops    *prometheus.CounterVec
	Winners        *prometheus.CounterVec
	MarginBps      *prometheus.HistogramVec
}

// NewSwap creates and registers swap metrics with the given registerer.
func NewSwap(reg prometheus.Registerer) *Swap {
	s := &Swap{
		SourceRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "freighter_swap_source_requests_total",
			Help: "Swap source calls by source, op (quote|build) and result (ok|no_route|insufficient_balance|rate_limited|timeout|check_failed|quote_moved|error).",
		}, []string{"source", "op", "result"}),
		UnknownHops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "freighter_swap_lifi_unknown_hop_total",
			Help: "LI.FI route hops the backend could not read, by venue.",
		}, []string{"venue"}),
		Winners: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "freighter_swap_quote_winner_total",
			Help: "Winning source when two or more sources quoted, by pair kind (classic|soroban).",
		}, []string{"winner", "pair"}),
		MarginBps: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "freighter_swap_quote_margin_bps",
			Help:    "How far the winning quote beat the runner-up, in basis points.",
			Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
		}, []string{"winner"}),
	}
	reg.MustRegister(s.SourceRequests, s.UnknownHops, s.Winners, s.MarginBps)
	return s
}

// RecordSwapSource records one swap source call outcome. Nil-safe.
func RecordSwapSource(m *Swap, source, op, result string) {
	if m == nil {
		return
	}
	m.SourceRequests.WithLabelValues(source, op, result).Inc()
}

// RecordSwapUnknownHop records an LI.FI hop the backend could not read.
// Venues outside the closed set are bucketed as "other". Nil-safe.
func RecordSwapUnknownHop(m *Swap, venue string) {
	if m == nil {
		return
	}
	if !knownHopVenues[venue] {
		venue = "other"
	}
	m.UnknownHops.WithLabelValues(venue).Inc()
}

// RecordSwapWinner records the winning source of a multi-source quote and the
// margin over the runner-up in basis points. Nil-safe.
func RecordSwapWinner(m *Swap, winner, pair string, marginBps float64) {
	if m == nil {
		return
	}
	m.Winners.WithLabelValues(winner, pair).Inc()
	m.MarginBps.WithLabelValues(winner).Observe(marginBps)
}
