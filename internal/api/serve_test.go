package api

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
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

func TestApiServer_initServices_RequiresAPIKey(t *testing.T) {
	s := &ApiServer{
		cfg:        &config.Config{},
		appMetrics: metrics.NewMetrics(prometheus.NewRegistry()),
	}
	err := s.initServices()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "STELLAR_EXPERT_API_KEY")
}

func TestApiServer_initServices_AcceptsAPIKey(t *testing.T) {
	s := &ApiServer{
		cfg: &config.Config{
			PricesConfig: config.PricesConfig{StellarExpertAPIKey: "test-key"},
		},
		appMetrics: metrics.NewMetrics(prometheus.NewRegistry()),
	}
	err := s.initServices()
	assert.NoError(t, err)
}

func TestApiServer_initHandlers(t *testing.T) {
	reg := prometheus.NewRegistry()
	cfg := &config.Config{AppConfig: config.AppConfig{ProtocolsConfigPath: "testdata/protocols.json"}}
	s := &ApiServer{cfg: cfg, registry: reg}
	mux := s.initHandlers()
	require.NotNil(t, mux)
	handler, pattern := mux.Handler(&http.Request{Method: "GET", URL: mustParseURL("/api/v1/ping")})
	assert.NotNil(t, handler)
	assert.Contains(t, pattern, "/api/v1/ping")
	healthHandler := handlers.NewHealthHandler()
	assert.IsType(t, handlers.CustomHandler(healthHandler.CheckHealth), handler, "returned handler should be the health check handler")
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

func mustParseURL(path string) *url.URL {
	u, err := url.Parse(path)
	if err != nil {
		panic(err)
	}
	return u
}
