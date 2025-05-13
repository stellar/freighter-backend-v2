package api

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
	"github.com/stellar/freighter-backend-v2/internal/config"
)

func TestNewApiServer(t *testing.T) {
	cfg := &config.Config{}
	s := NewApiServer(cfg)
	require.NotNil(t, s)
	assert.Equal(t, cfg, s.cfg)
}

func TestApiServer_initServices_Error(t *testing.T) {
	s := &ApiServer{cfg: &config.Config{RedisConfig: config.RedisConfig{Host: "", Port: 0}}}
	err := s.initServices()
	assert.NoError(t, err)
}

func TestApiServer_initHandlers(t *testing.T) {
	cfg := &config.Config{AppConfig: config.AppConfig{ProtocolsConfigPath: "testdata/protocols.json"}}
	s := &ApiServer{cfg: cfg}
	mux := s.initHandlers()
	require.NotNil(t, mux)
	handler, pattern := mux.Handler(&http.Request{Method: "GET", URL: mustParseURL("/api/v1/ping")})
	assert.NotNil(t, handler)
	assert.Contains(t, pattern, "/api/v1/ping")
	assert.IsType(t, handlers.CustomHandler(handlers.HealthCheckHandler), handler, "returned handler should be the health check handler")
}

func TestApiServer_initMiddleware(t *testing.T) {
	s := &ApiServer{}
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
