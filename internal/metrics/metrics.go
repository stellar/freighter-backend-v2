// ABOUTME: Defines all Prometheus metric collectors for the Freighter backend.
// ABOUTME: Groups HTTP request metrics and external service call metrics into a single Metrics struct.
package metrics

import (
	"context"
	"errors"
	"net"
	"net/url"

	"github.com/creachadair/jrpc2"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics groups all Prometheus metrics. Must be created via NewMetrics.
type Metrics struct {
	HTTP    *HTTP
	Service *Service
}

// NewMetrics creates and registers all application metrics with the given registerer.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	return &Metrics{
		HTTP:    NewHTTP(reg),
		Service: NewService(reg),
	}
}

// HTTP holds HTTP request metrics.
type HTTP struct {
	// RequestsTotal counts completed HTTP requests, labeled by handler pattern, method, and status code.
	RequestsTotal *prometheus.CounterVec
	// RequestDuration observes request latency in seconds as a histogram, with the same labels as RequestsTotal.
	RequestDuration *prometheus.HistogramVec
	// InFlightRequests is a gauge tracking the number of HTTP requests currently being processed.
	// It increments on request entry and decrements on response completion.
	InFlightRequests prometheus.Gauge
}

// NewHTTP creates and registers HTTP metrics with the given registerer.
func NewHTTP(reg prometheus.Registerer) *HTTP {
	h := &HTTP{
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "freighter_http_requests_total",
			Help: "Total number of HTTP requests.",
		}, []string{"handler", "method", "code"}),
		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "freighter_http_request_duration_seconds",
			Help:    "Duration of HTTP requests in seconds.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"handler", "method", "code"}),
		InFlightRequests: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "freighter_http_in_flight_requests",
			Help: "Number of HTTP requests currently being processed.",
		}),
	}
	reg.MustRegister(h.RequestsTotal, h.RequestDuration, h.InFlightRequests)
	return h
}

// Service holds external service call metrics (RPC, wallet-backend, etc.).
type Service struct {
	// CallsTotal counts completed external service calls, labeled by service name, method, and network.
	CallsTotal *prometheus.CounterVec
	// CallDuration observes service call latency in seconds as a histogram, with the same labels as CallsTotal.
	CallDuration *prometheus.HistogramVec
	// ErrorsTotal counts failed service calls, labeled by service name, method, network, and error_type.
	// Error types: "timeout" (context deadline/canceled), "connection" (network/dial failures),
	// "upstream" (JSON-RPC or HTTP error responses from dependencies), "internal" (encoding/validation).
	ErrorsTotal *prometheus.CounterVec
}

// NewService creates and registers service call metrics with the given registerer.
func NewService(reg prometheus.Registerer) *Service {
	s := &Service{
		CallsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "freighter_service_calls_total",
			Help: "Total number of external service calls.",
		}, []string{"service", "method", "network"}),
		CallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "freighter_service_call_duration_seconds",
			Help:    "Duration of external service calls in seconds.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"service", "method", "network"}),
		ErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "freighter_service_errors_total",
			Help: "Total number of external service call errors.",
		}, []string{"service", "method", "network", "error_type"}),
	}
	reg.MustRegister(s.CallsTotal, s.CallDuration, s.ErrorsTotal)
	return s
}

// ClassifyError categorizes a service call error for the error_type metric label.
//   - "timeout":    context deadline exceeded or canceled
//   - "connection": network-level failures (dial, DNS, connection refused)
//   - "upstream":   the upstream service returned an error response (JSON-RPC error or HTTP non-2xx)
//   - "internal":   local failures (encoding, decoding, validation)
func ClassifyError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}

	var netErr *net.OpError
	var urlErr *url.Error
	if errors.As(err, &netErr) || errors.As(err, &urlErr) {
		return "connection"
	}

	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return "upstream"
	}

	return "internal"
}
