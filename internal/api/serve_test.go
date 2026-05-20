package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

func TestNewApiServer(t *testing.T) {
	cfg := &config.Config{}
	s := NewApiServer(cfg)
	require.NotNil(t, s)
	assert.Equal(t, cfg, s.cfg)
}

func TestApiServer_initServices_Success(t *testing.T) {
	// Minimal but valid config: WalletBackendBalanceConcurrency must be > 0
	// because NewWalletBackendService rejects non-positive values to avoid
	// errgroup.SetLimit(0)/(-1) surprises.
	s := &ApiServer{
		cfg: &config.Config{
			RedisConfig: config.RedisConfig{Host: "", Port: 0},
			AppConfig:   config.AppConfig{WalletBackendBalanceConcurrency: 10},
		},
		appMetrics: metrics.NewMetrics(prometheus.NewRegistry()),
	}
	err := s.initServices()
	assert.NoError(t, err)
}

func TestApiServer_initServices_RejectsNonPositiveBalanceConcurrency(t *testing.T) {
	for _, n := range []int{0, -1} {
		s := &ApiServer{
			cfg: &config.Config{
				AppConfig: config.AppConfig{WalletBackendBalanceConcurrency: n},
			},
			appMetrics: metrics.NewMetrics(prometheus.NewRegistry()),
		}
		err := s.initServices()
		require.Error(t, err, "expected error for WalletBackendBalanceConcurrency=%d", n)
		assert.Contains(t, err.Error(), "maxBalanceConcurrency")
	}
}

func TestApiServer_initHandlers(t *testing.T) {
	reg := prometheus.NewRegistry()
	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
	}}
	s := &ApiServer{cfg: cfg, registry: reg}
	mux, err := s.initHandlers()
	require.NoError(t, err)
	require.NotNil(t, mux)
	handler, pattern := mux.Handler(&http.Request{Method: "GET", URL: mustParseURL("/api/v1/ping")})
	assert.NotNil(t, handler)
	assert.Contains(t, pattern, "/api/v1/ping")
	healthHandler := handlers.NewHealthHandler()
	assert.IsType(t, handlers.CustomHandler(healthHandler.CheckHealth), handler, "returned handler should be the health check handler")
}

func TestApiServer_initHandlers_DoesNotServeMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
	}}
	s := &ApiServer{cfg: cfg, registry: reg}
	mux, err := s.initHandlers()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "/metrics must not be reachable on the public API mux")
}

func TestApiServer_initMetricsHandler(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	s := &ApiServer{cfg: &config.Config{}, registry: reg}

	handler := s.initMetricsHandler()
	require.NotNil(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "# HELP go_")
}

func TestApiServer_initMetricsHandler_RejectsOtherPaths(t *testing.T) {
	s := &ApiServer{cfg: &config.Config{}, registry: prometheus.NewRegistry()}
	handler := s.initMetricsHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "metrics server must only expose /metrics")
}

func TestApiServer_initMiddleware(t *testing.T) {
	s := &ApiServer{
		cfg:        &config.Config{},
		appMetrics: metrics.NewMetrics(prometheus.NewRegistry()),
	}
	mux := http.NewServeMux()
	h := s.initMiddleware(mux)
	require.NotNil(t, h)
}

func TestApiServer_initHandlers_RegistersAccountHistoryRoutes(t *testing.T) {
	reg := prometheus.NewRegistry()
	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
	}}
	s := &ApiServer{cfg: cfg, registry: reg}
	mux, err := s.initHandlers()
	require.NoError(t, err)
	require.NotNil(t, mux)

	for _, path := range []string{
		"/api/v1/accounts/GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF/transactions",
		"/api/v1/accounts/GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF/operations",
		"/api/v1/accounts/GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF/state-changes",
	} {
		handler, pattern := mux.Handler(&http.Request{Method: "GET", URL: mustParseURL(path)})
		assert.NotNil(t, handler, "no handler registered for %s", path)
		assert.NotEmpty(t, pattern, "no pattern matched for %s", path)
	}
}

func TestApiServer_initHandlers_ReturnsErrorOnInvalidConfig(t *testing.T) {
	reg := prometheus.NewRegistry()
	// Zero AccountHistoryDefaultLimit makes the constructor fail.
	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath: "testdata/protocols.json",
	}}
	s := &ApiServer{cfg: cfg, registry: reg}
	mux, err := s.initHandlers()
	require.Error(t, err)
	assert.Nil(t, mux)
	assert.Contains(t, err.Error(), "init account-history handler")
}

func mustParseURL(path string) *url.URL {
	u, err := url.Parse(path)
	if err != nil {
		panic(err)
	}
	return u
}
