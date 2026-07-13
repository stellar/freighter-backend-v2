package api

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	jwtgo "github.com/golang-jwt/jwt/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// newTestAPIServer builds a fully-initialized ApiServer for handler-level tests
// (registry, metrics, and resolved auth mode), mirroring what Start() sets up so
// initHandlers can rely on the same non-nil invariants as production.
func newTestAPIServer(t *testing.T, cfg *config.Config) *ApiServer {
	t.Helper()
	reg := prometheus.NewRegistry()
	mode, err := auth.ParseMode(cfg.AppConfig.AuthMode)
	require.NoError(t, err)
	return &ApiServer{
		cfg:        cfg,
		registry:   reg,
		appMetrics: metrics.NewMetrics(reg),
		authMode:   mode,
	}
}

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
	// Minimal but valid config: WalletBackendBalanceConcurrency must be > 0
	// because NewWalletBackendService rejects non-positive values to avoid
	// errgroup.SetLimit(0)/(-1) surprises.
	s := &ApiServer{
		cfg: &config.Config{
			PricesConfig: config.PricesConfig{StellarExpertAPIKey: "test-key"},
			AppConfig:    config.AppConfig{WalletBackendBalanceConcurrency: 10},
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
				PricesConfig: config.PricesConfig{StellarExpertAPIKey: "test-key"},
				AppConfig:    config.AppConfig{WalletBackendBalanceConcurrency: n},
			},
			appMetrics: metrics.NewMetrics(prometheus.NewRegistry()),
		}
		err := s.initServices()
		require.Error(t, err, "expected error for WalletBackendBalanceConcurrency=%d", n)
		assert.Contains(t, err.Error(), "maxBalanceConcurrency")
	}
}

func TestApiServer_initHandlers(t *testing.T) {
	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
		AuthMode:                   "permissive",
	}}
	s := newTestAPIServer(t, cfg)
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
	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
		AuthMode:                   "permissive",
	}}
	s := newTestAPIServer(t, cfg)
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
	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
		AuthMode:                   "permissive",
	}}
	s := newTestAPIServer(t, cfg)
	mux, err := s.initHandlers()
	require.NoError(t, err)
	require.NotNil(t, mux)

	path := "/api/v1/accounts/GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF/transactions"
	handler, pattern := mux.Handler(&http.Request{Method: "GET", URL: mustParseURL(path)})
	assert.NotNil(t, handler, "no handler registered for %s", path)
	assert.NotEmpty(t, pattern, "no pattern matched for %s", path)
}

func TestApiServer_initHandlers_WhoamiRouteRespectsAuthMode(t *testing.T) {
	cfgWith := func(authMode string) *config.Config {
		return &config.Config{AppConfig: config.AppConfig{
			ProtocolsConfigPath:        "testdata/protocols.json",
			AccountHistoryDefaultLimit: 20,
			AccountHistoryMaxLimit:     100,
			AuthMode:                   authMode,
		}}
	}

	// Permissive: an unauthenticated request passes through.
	mux, err := newTestAPIServer(t, cfgWith("permissive")).initHandlers()
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/whoami", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "\"authenticated\":false")

	// Strict: an unauthenticated request is rejected.
	mux, err = newTestAPIServer(t, cfgWith("strict")).initHandlers()
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/whoami", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestApiServer_initHandlers_ReturnsErrorOnInvalidConfig(t *testing.T) {
	// Zero AccountHistoryDefaultLimit makes the constructor fail.
	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath: "testdata/protocols.json",
		AuthMode:            "permissive",
	}}
	s := newTestAPIServer(t, cfg)
	mux, err := s.initHandlers()
	require.Error(t, err)
	assert.Nil(t, mux)
	assert.Contains(t, err.Error(), "init account-history handler")
}

func TestApiServer_initHandlers_UserFacingRoutesRespectAuthMode(t *testing.T) {
	cfgWith := func(authMode string) *config.Config {
		return &config.Config{AppConfig: config.AppConfig{
			ProtocolsConfigPath:        "testdata/protocols.json",
			AccountHistoryDefaultLimit: 20,
			AccountHistoryMaxLimit:     100,
			AuthMode:                   authMode,
		}}
	}

	// feature-flags is a dependency-free user-facing GET that returns 200 with no
	// file or service, so it isolates auth behavior from handler dependencies.

	// Permissive: an anonymous request to a user-facing route passes through.
	mux, err := newTestAPIServer(t, cfgWith("permissive")).initHandlers()
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/feature-flags", nil))
	assert.Equal(t, http.StatusOK, rec.Code, "permissive: anonymous must pass")

	// Permissive: a present-but-invalid bearer token is rejected on that route.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/feature-flags", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "permissive: invalid token must 401")

	// Strict: an anonymous request to a previously-open route is rejected.
	mux, err = newTestAPIServer(t, cfgWith("strict")).initHandlers()
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/feature-flags", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "strict: anonymous must 401")
}

func mustParseURL(path string) *url.URL {
	u, err := url.Parse(path)
	if err != nil {
		panic(err)
	}
	return u
}

// stubRPCService is a no-network RPCService double so /rpc-health can be invoked
// in tests without a live RPC endpoint. Only GetHealth is used by CheckRPCHealth.
type stubRPCService struct{}

func (stubRPCService) Name() string { return "stub-rpc" }

func (stubRPCService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	return types.GetHealthResponse{Status: types.StatusHealthy}, nil
}

func (stubRPCService) SimulateTx(ctx context.Context, tx *txnbuild.Transaction, network string) (types.SimulateTransactionResponse, error) {
	return nil, nil
}

func (stubRPCService) SimulateInvocation(ctx context.Context, contractId xdr.ScAddress, sourceAccount *txnbuild.SimpleAccount, functionName xdr.ScSymbol, params []xdr.ScVal, timeout txnbuild.TimeBounds, network string) (types.SimulateTransactionResponse, error) {
	return nil, nil
}

func (stubRPCService) GetLedgerEntries(ctx context.Context, keys []string, network string) ([]types.LedgerEntryMap, error) {
	return nil, nil
}

func TestApiServer_initHandlers_HealthRoutesAnonymousInStrict(t *testing.T) {
	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
		AuthMode:                   "strict",
	}}
	s := newTestAPIServer(t, cfg)
	s.rpcService = stubRPCService{} // so /rpc-health can be invoked without a live RPC

	mux, err := s.initHandlers()
	require.NoError(t, err)

	for _, path := range []string{"/api/v1/ping", "/api/v1/db-health", "/api/v1/rpc-health"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.NotEqual(t, http.StatusUnauthorized, rec.Code,
			"%s must stay anonymous (no 401) even in strict mode", path)
		assert.Equal(t, http.StatusOK, rec.Code, "%s should return 200", path)
	}
}

// mintAPIToken mints a valid Ed25519 JWT bound to a specific method and path.
// It mirrors the proven token minter in internal/api/middleware/auth_test.go.
func mintAPIToken(t *testing.T, priv ed25519.PrivateKey, sub, methodAndPath string) string {
	t.Helper()
	now := time.Now()
	c := auth.Claims{
		BodyHash:      auth.HashBody(nil), // GET requests have no body
		MethodAndPath: methodAndPath,
		RegisteredClaims: jwtgo.RegisteredClaims{
			Subject:   sub,
			Issuer:    "freighter-extension",
			IssuedAt:  jwtgo.NewNumericDate(now),
			ExpiresAt: jwtgo.NewNumericDate(now.Add(auth.MaxTokenLifetime)),
		},
	}
	s, err := jwtgo.NewWithClaims(jwtgo.SigningMethodEdDSA, c).SignedString(priv)
	require.NoError(t, err)
	return s
}

func TestApiServer_initHandlers_ValidTokenPopulatesWhoami(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
		AuthMode:                   "permissive",
	}}
	mux, err := newTestAPIServer(t, cfg).initHandlers()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+mintAPIToken(t, priv, sub, "GET /api/v1/auth/whoami"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "\"authenticated\":true")
	assert.Contains(t, rec.Body.String(), sub)
}

func TestApiServer_authenticatedRequestKeepsRouteMetricLabel(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
		AuthMode:                   "permissive",
	}}
	s := newTestAPIServer(t, cfg)
	mux, err := s.initHandlers()
	require.NoError(t, err)
	assembled := s.initMiddleware(mux) // full chain incl. Metrics, so r.Pattern is exercised

	req := httptest.NewRequest(http.MethodGet, "/api/v1/feature-flags", nil)
	req.Header.Set("Authorization", "Bearer "+mintAPIToken(t, priv, sub, "GET /api/v1/feature-flags"))
	rec := httptest.NewRecorder()
	assembled.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// The authenticated request must be metered under its real route pattern,
	// not "unknown" (which is what a pre-routing context fork would have caused).
	got := testutil.ToFloat64(s.appMetrics.HTTP.RequestsTotal.WithLabelValues("GET /api/v1/feature-flags", "GET", "200"))
	assert.Equal(t, float64(1), got, "authed request should be counted under its route pattern")
	unknown := testutil.ToFloat64(s.appMetrics.HTTP.RequestsTotal.WithLabelValues("unknown", "GET", "200"))
	assert.Equal(t, float64(0), unknown, "authed request must not collapse to the unknown handler label")
}

// TestApiServer_initHandlers_AllUserFacingRoutesGatedInStrict guards the full set
// of user-facing routes against one being added (or left) without the shared
// authed wrapper — a silent fail-open. In strict mode the Auth middleware rejects
// an anonymous request with 401 BEFORE the handler runs, so this needs no service
// stubs: the nil services left by newTestAPIServer are never reached. A route
// registered bare would instead reach its handler (returning 200/404, or panicking
// on a nil service) and fail this assertion. Health probes are covered separately
// by TestApiServer_initHandlers_HealthRoutesAnonymousInStrict.
func TestApiServer_initHandlers_AllUserFacingRoutesGatedInStrict(t *testing.T) {
	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
		AuthMode:                   "strict",
	}}
	mux, err := newTestAPIServer(t, cfg).initHandlers()
	require.NoError(t, err)

	userFacing := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/protocols"},
		{http.MethodPost, "/api/v1/collectibles"},
		{http.MethodPost, "/api/v1/ledger-key/accounts"},
		{http.MethodGet, "/api/v1/feature-flags"},
		{http.MethodPost, "/api/v1/accounts/balances"},
		{http.MethodPost, "/api/v1/token-prices"},
		{http.MethodGet, "/api/v1/accounts/GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF/transactions"},
		{http.MethodGet, "/api/v1/auth/whoami"},
	}
	for _, r := range userFacing {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(r.method, r.path, nil))
		assert.Equal(t, http.StatusUnauthorized, rec.Code,
			"%s %s must run the auth middleware (401 for anonymous in strict)", r.method, r.path)
	}
}
