// ABOUTME: Defines all Prometheus metric collectors and the Record helper for the Freighter backend.
// ABOUTME: Groups HTTP request metrics and external service call metrics into a single Metrics struct.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"

	"github.com/creachadair/jrpc2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// sanitizeNetwork buckets any value that is not one of the finite, supported
// networks into a single "unknown" label. The network label flows into
// Prometheus metric vectors, which retain a time series per unique label value
// for the lifetime of the process. Bucketing here caps the cardinality of the
// network label so a caller-controlled value can never cause unbounded series
// (and thus unbounded memory) growth, independent of any handler-level
// validation.
func sanitizeNetwork(network string) string {
	switch network {
	case types.PUBLIC, types.TESTNET, types.FUTURENET:
		return network
	default:
		return "unknown"
	}
}

// UpstreamError represents an error from an upstream service that should be
// classified with a specific kind and optional error code for metric labels.
type UpstreamError struct {
	Kind string // "simulation_error", "graphql_error", "http_error"
	Code int    // optional: HTTP status code, 0 if not applicable
	Err  error
}

func (e *UpstreamError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("%s (code %d): %v", e.Kind, e.Code, e.Err)
	}
	return fmt.Sprintf("%s: %v", e.Kind, e.Err)
}

func (e *UpstreamError) Unwrap() error { return e.Err }

// Metrics groups all Prometheus metrics. Must be created via NewMetrics.
type Metrics struct {
	HTTP    *HTTP
	Service *Service
	Auth    *Auth
	Prices  *Prices
}

// NewMetrics creates and registers all application metrics with the given registerer.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	return &Metrics{
		HTTP:    NewHTTP(reg),
		Service: NewService(reg),
		Auth:    NewAuth(reg),
		Prices:  NewPrices(reg),
	}
}

// Client-type label values for the auth metric. The set is closed so a
// client-controlled `iss` cannot grow label cardinality without bound.
const (
	clientExtension = "freighter-extension"
	clientMobile    = "freighter-mobile"
	// ClientNone labels a request that carried no token at all (permissive
	// anonymous path). Kept distinct from "other" so "has not adopted auth"
	// is never confused with "token present, client unknown".
	ClientNone  = "none"
	clientOther = "other"
)

// SanitizeClient buckets a JWT `iss` (client type) into the closed allowlist.
// Anything outside it — including "" — becomes "other". This bounds the
// cardinality of the client label, which derives from a client-controlled
// claim. The allowlist mirrors the design doc's `iss` contract; a client that
// ships a different `iss` shows up as "other" (a useful drift canary).
func SanitizeClient(iss string) string {
	switch iss {
	case clientExtension, clientMobile:
		return iss
	default:
		return clientOther
	}
}

// Auth holds JWT authentication metrics. During the client rollout these track
// adoption (authenticated vs anonymous), rejection reasons, and client type.
type Auth struct {
	// RequestsTotal counts auth-checked requests, labeled by outcome, reason,
	// and client.
	//   result: "authenticated" | "anonymous" | "rejected"
	//   reason: "ok" | "no_token" | "expired" | "bad_signature" | "bad_timing" |
	//           "bad_method_path" | "bad_body_hash" | "bad_subject" | "malformed" |
	//           "invalid" | "too_large" | "internal"
	//   client: "freighter-extension" | "freighter-mobile" | "none" | "other"
	RequestsTotal *prometheus.CounterVec
}

// NewAuth creates and registers auth metrics with the given registerer.
func NewAuth(reg prometheus.Registerer) *Auth {
	a := &Auth{
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "freighter_auth_requests_total",
			Help: "Total number of auth-checked HTTP requests, by result, reason, and client.",
		}, []string{"result", "reason", "client"}),
	}
	reg.MustRegister(a.RequestsTotal)
	return a
}

// RecordAuth records the outcome of an auth check. It is nil-safe so the
// middleware and its tests can run without a metrics registry.
func RecordAuth(a *Auth, result, reason, client string) {
	if a == nil {
		return
	}
	a.RequestsTotal.WithLabelValues(result, reason, client).Inc()
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
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.25, 0.35, 0.5, 0.75, 1, 2.5, 5, 10, 30},
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
	// Error types: "timeout", "connection", "rpc_error:<code>", "simulation_error",
	// "graphql_error", "http_error[:<code>]", "internal".
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
			Buckets: []float64{0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.25, 0.35, 0.5, 0.75, 1, 2.5, 5, 10, 30},
		}, []string{"service", "method", "network"}),
		ErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "freighter_service_errors_total",
			Help: "Total number of external service call errors.",
		}, []string{"service", "method", "network", "error_type"}),
	}
	reg.MustRegister(s.CallsTotal, s.CallDuration, s.ErrorsTotal)
	return s
}

// Prices holds metrics specific to the token-prices service: cache outcomes,
// degraded-mode signals (miss-budget exhaustion), and
// Redis-from-this-service-POV errors.
type Prices struct {
	// CacheOutcomes counts per-token cache outcomes by network and outcome:
	// "hit" (live entry within --price-cache-ttl-seconds) or "miss" (no
	// entry, expired, or upstream-only path).
	CacheOutcomes *prometheus.CounterVec
	// MissBudgetExhausted counts requests whose miss-fetch budget
	// (--price-fetch-timeout-seconds) tripped before all misses resolved.
	// Labeled by network.
	MissBudgetExhausted *prometheus.CounterVec
	// RedisErrors counts Redis operations from the prices service that
	// failed (and were silently fallen-through). Labeled by op: "mget" or
	// "set".
	RedisErrors *prometheus.CounterVec
}

// NewPrices creates and registers prices-service metrics with the given registerer.
func NewPrices(reg prometheus.Registerer) *Prices {
	p := &Prices{
		CacheOutcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "freighter_prices_cache_outcomes_total",
			Help: "Per-token cache outcomes for the token-prices endpoint.",
		}, []string{"network", "outcome"}),
		MissBudgetExhausted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "freighter_prices_miss_budget_exhausted_total",
			Help: "Requests whose miss-fetch budget elapsed before all misses resolved.",
		}, []string{"network"}),
		RedisErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "freighter_prices_redis_errors_total",
			Help: "Redis operation failures observed by the prices service.",
		}, []string{"op"}),
	}
	reg.MustRegister(p.CacheOutcomes, p.MissBudgetExhausted, p.RedisErrors)
	return p
}

// Record records call metrics for a service method invocation.
// It is nil-safe: if m is nil, it is a no-op, allowing services to work without metrics in tests.
func Record(m *Service, service, method, network string, duration float64, err error) {
	if m == nil {
		return
	}
	network = sanitizeNetwork(network)
	m.CallsTotal.WithLabelValues(service, method, network).Inc()
	m.CallDuration.WithLabelValues(service, method, network).Observe(duration)
	if err != nil {
		m.ErrorsTotal.WithLabelValues(service, method, network, ClassifyError(err)).Inc()
	}
}

// ClassifyError categorizes a service call error for the error_type metric label.
//   - "timeout":              context deadline exceeded or canceled
//   - "connection":           network-level failures (dial, DNS, connection refused)
//   - "simulation_error":     RPC simulation returned error string
//   - "graphql_error":        GraphQL response errors from wallet-backend
//   - "http_error" / "http_error:<code>": HTTP 4xx/5xx (code when available)
//   - "rpc_error:<code>":     jrpc2 errors with JSON-RPC error code
//   - "internal":             local failures (encoding, decoding, validation)
func ClassifyError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}

	var netErr *net.OpError
	var urlErr *url.Error
	if errors.As(err, &netErr) || errors.As(err, &urlErr) {
		return "connection"
	}

	var upErr *UpstreamError
	if errors.As(err, &upErr) {
		if upErr.Code != 0 {
			return fmt.Sprintf("%s:%d", upErr.Kind, upErr.Code)
		}
		return upErr.Kind
	}

	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return fmt.Sprintf("rpc_error:%d", rpcErr.Code)
	}

	return "internal"
}
