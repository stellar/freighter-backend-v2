package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
	"github.com/stellar/freighter-backend-v2/internal/api/middleware"
	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/db"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/services"
	"github.com/stellar/freighter-backend-v2/internal/store"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	DefaultReadTimeout    = 10 * time.Second
	DefaultWriteTimeout   = 10 * time.Second
	DefaultIdleTimeout    = 120 * time.Second
	ServerShutdownTimeout = 10 * time.Second
	// DatabaseConnectTimeout bounds the boot-time connect + ping so a reachable
	// but unresponsive database (LB misroute, network blackhole) fails startup
	// fast instead of hanging the process indefinitely.
	DatabaseConnectTimeout = 30 * time.Second
)

type ApiServer struct {
	cfg                  *config.Config
	redis                *store.RedisStore
	dbPool               *pgxpool.Pool
	rpcService           types.RPCService
	walletBackendService types.WalletBackendService
	pricesService        types.PricesService
	registry             *prometheus.Registry
	appMetrics           *metrics.Metrics
	authMode             auth.Mode
}

func NewApiServer(cfg *config.Config) *ApiServer {
	return &ApiServer{cfg: cfg}
}

func (s *ApiServer) Start() error {
	// Resolve the auth mode once, up front, so a misconfigured value fails before
	// we bind ports or open connections. Downstream code reads s.authMode.
	authMode, err := auth.ParseMode(s.cfg.AppConfig.AuthMode)
	if err != nil {
		return fmt.Errorf("parsing auth mode: %w", err)
	}
	s.authMode = authMode

	s.registry = prometheus.NewRegistry()
	s.registry.MustRegister(collectors.NewGoCollector())
	s.registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	s.registry.MustRegister(collectors.NewBuildInfoCollector())
	s.appMetrics = metrics.NewMetrics(s.registry)

	if err = s.initServices(); err != nil {
		logger.Error("Failed to initialize services", "error", err)
		return err
	}

	// The database is initialized separately from initServices because it eagerly
	// connects (to fail fast), whereas the other clients are lazy. Keeping it out
	// of initServices keeps that function unit-testable without real infrastructure.
	// Skipped entirely when the DB is disabled: no pool is opened and dbPool stays
	// nil, so DB-backed features (and db-health) report unavailable.
	if s.cfg.DatabaseConfig.Enabled {
		if err = s.initDatabase(); err != nil {
			logger.Error("Failed to initialize database", "error", err)
			return err
		}
	} else {
		logger.Warn("Database is disabled (--db-enabled=false); running without a database")
	}
	defer s.closeServices()

	mux, err := s.initHandlers()
	if err != nil {
		return fmt.Errorf("initializing handlers: %w", err)
	}
	apiHandler := s.initMiddleware(mux)
	metricsHandler := middleware.Chain(s.initMetricsHandler(), middleware.Recover())
	return s.startServers(apiHandler, metricsHandler)
}

func (s *ApiServer) initServices() error {
	if s.cfg.PricesConfig.StellarExpertAPIKey == "" {
		return fmt.Errorf("STELLAR_EXPERT_API_KEY is required")
	}

	s.redis = store.NewRedisStore(s.cfg.RedisConfig.Host, s.cfg.RedisConfig.Port, s.cfg.RedisConfig.Password)

	s.rpcService = services.NewRPCService(s.cfg.RpcConfig.PubnetRpcUrl, s.cfg.RpcConfig.TestnetRpcUrl, s.cfg.RpcConfig.FuturenetRpcUrl, s.appMetrics.Service)

	// Initialize wallet backend service if configured
	walletBackendService, err := services.NewWalletBackendService(
		s.cfg.WalletBackendConfig.PubnetUrl,
		s.cfg.WalletBackendConfig.TestnetUrl,
		s.cfg.WalletBackendConfig.PubnetSigningKey,
		s.cfg.WalletBackendConfig.TestnetSigningKey,
		s.cfg.AppConfig.WalletBackendBalanceConcurrency,
		s.appMetrics.Service,
	)
	if err != nil {
		logger.Error("Failed to initialize wallet backend service", "error", err)
		return err
	}
	s.walletBackendService = walletBackendService

	stellarExpert := services.NewStellarExpertService(
		s.cfg.PricesConfig.StellarExpertPubnetURL,
		s.cfg.PricesConfig.StellarExpertTestnetURL,
		s.cfg.PricesConfig.StellarExpertAPIKey,
		s.cfg.PricesConfig.StellarExpertOrigin,
		s.appMetrics.Service,
	)
	s.pricesService = services.NewPricesService(stellarExpert, s.redis, services.PricesServiceConfig{
		CacheTTL:         time.Duration(s.cfg.PricesConfig.PriceCacheTTLSeconds) * time.Second,
		MissFetchTimeout: time.Duration(s.cfg.PricesConfig.PriceFetchTimeoutSeconds) * time.Second,
		MaxConcurrent:    s.cfg.PricesConfig.MaxConcurrentPriceFetches,
	}, s.appMetrics.Service, s.appMetrics.Prices)

	return nil
}

// initDatabase opens the long-lived connection pool and pings it so a
// misconfigured or unreachable database aborts startup. Migrations are NOT run
// here: they are applied out-of-band via the `migrate` subcommand (a deploy Job
// or a manual step), so booting serve never mutates a shared database's schema.
func (s *ApiServer) initDatabase() error {
	ctx, cancel := context.WithTimeout(context.Background(), DatabaseConnectTimeout)
	defer cancel()

	pool, err := db.OpenDBConnectionPool(ctx, s.cfg.DatabaseConfig.URL, db.PoolConfig{
		MaxConns:        int32(s.cfg.DatabaseConfig.MaxConns), //nolint:gosec // operator-supplied pool size, within int32
		MinConns:        int32(s.cfg.DatabaseConfig.MinConns), //nolint:gosec // operator-supplied pool size, within int32
		MaxConnLifetime: s.cfg.DatabaseConfig.MaxConnLifetime,
		MaxConnIdleTime: s.cfg.DatabaseConfig.MaxConnIdleTime,
		// QueryExecMode is left at pgx's default (CacheStatement): we connect
		// directly to the CNPG primary (-rw), with no transaction-mode pooler in
		// front, so server-side prepared-statement caching is safe and faster.
	})
	if err != nil {
		logger.Error("Failed to open database connection pool", "error", err)
		return fmt.Errorf("opening database connection pool: %w", err)
	}
	s.dbPool = pool

	// Expose pool saturation/exhaustion to Prometheus so a pool starved by load
	// (the failure mode db-health surfaces) is observable, not just inferable.
	metrics.RegisterDBPoolMetrics(s.registry, s.dbPool)

	return nil
}

// closeServices releases service-level resources during shutdown.
func (s *ApiServer) closeServices() {
	if s.dbPool != nil {
		s.dbPool.Close()
	}
}

// route describes a single registered API endpoint. It is the single source of
// truth consumed by both initHandlers (which registers it) and the strict-mode
// gating guard test (which enumerates it): gated routes are wrapped in the Auth
// middleware, bare routes are not. Keeping method/pattern/gated together in one
// table means a newly-added route cannot silently skip the auth guard — it must
// declare `gated` here, and the test derives its coverage from the same table
// rather than a hand-maintained parallel list that could drift.
type route struct {
	method  string
	pattern string
	handler http.Handler
	gated   bool
}

// routes builds the full endpoint table. It constructs each handler with its
// dependencies but performs no middleware wrapping or mux registration, so both
// initHandlers and tests can enumerate the routes (and their gated flags) without
// standing up the auth middleware.
func (s *ApiServer) routes() ([]route, error) {
	// Pass a true nil interface when the DB is disabled — a nil *pgxpool.Pool
	// boxed in the interface is not == nil, which would defeat the handler's
	// disabled check.
	var dbPinger handlers.DBPinger
	if s.dbPool != nil {
		dbPinger = s.dbPool
	}

	healthHandler := handlers.NewHealthHandler()
	rpcHealthHandler := handlers.NewRPCHealthHandler(s.rpcService)
	dbHealthHandler := handlers.NewDBHealthHandler(dbPinger)

	protocolsHandler := handlers.NewProtocolsHandler(s.cfg.AppConfig.ProtocolsConfigPath)
	collectiblesHandler := handlers.NewCollectiblesHandler(s.rpcService, s.cfg.AppConfig.MeridianPayTreasureHuntAddress, s.cfg.AppConfig.MeridianPayTreasurePoapAddress, s.cfg.AppConfig.MeridianPayStellarHouseAddress, s.cfg.RpcConfig.MaxConcurrentRPCCalls)
	ledgerKeyAccountsHandler := handlers.NewLedgerKeyAccountHandler(s.rpcService, s.cfg.AppConfig.MaxLedgerKeyAddresses)
	featureFlagsHandler := handlers.NewFeatureFlagsHandler()
	accountBalancesHandler := handlers.NewAccountBalancesHandler(s.walletBackendService, s.cfg.AppConfig.MaxBalanceAddresses)
	tokenPricesHandler := handlers.NewTokenPricesHandler(s.pricesService, s.cfg.PricesConfig.MaxTokensPerRequest)
	accountHistoryHandler, err := handlers.NewAccountHistoryHandler(
		s.walletBackendService,
		s.cfg.AppConfig.AccountHistoryDefaultLimit,
		s.cfg.AppConfig.AccountHistoryMaxLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("init account-history handler: %w", err)
	}
	whoamiHandler := handlers.NewWhoamiHandler()

	positionsService := services.NewPositionsService(
		s.walletBackendService,
		s.redis,
		time.Duration(s.cfg.BlendConfig.PositionsCacheTTLSeconds)*time.Second,
		s.appMetrics.Service,
	)
	accountPositionsHandler := handlers.NewAccountPositionsHandler(positionsService)

	return []route{
		// Health/liveness/readiness probes: gated=false, registered BARE — never
		// wrapped by Auth. K8s and the docker-compose wget healthcheck cannot present
		// per-request JWTs, and db-health is designed never to fail the request;
		// gating any of these would 401 probes under `--auth-mode strict` and cause
		// pod churn.
		{http.MethodGet, "/api/v1/ping", handlers.CustomHandler(healthHandler.CheckHealth), false},
		{http.MethodGet, "/api/v1/rpc-health", handlers.CustomHandler(rpcHealthHandler.CheckRPCHealth), false},
		{http.MethodGet, "/api/v1/db-health", handlers.CustomHandler(dbHealthHandler.CheckDBHealth), false},

		// User-facing routes: gated=true, wrapped in the shared Auth middleware.
		// Flipping --auth-mode permissive<->strict moves all of these together.
		// whoami reads the user ID from context and reports authenticated:false when
		// absent (permissive anonymous).
		{http.MethodGet, "/api/v1/protocols", handlers.CustomHandler(protocolsHandler.GetProtocols), true},
		{http.MethodPost, "/api/v1/collectibles", handlers.CustomHandler(collectiblesHandler.GetCollectibles), true},
		{http.MethodPost, "/api/v1/ledger-key/accounts", handlers.CustomHandler(ledgerKeyAccountsHandler.GetLedgerKeyAccounts), true},
		{http.MethodGet, "/api/v1/feature-flags", handlers.CustomHandler(featureFlagsHandler.GetFeatureFlags), true},
		{http.MethodPost, "/api/v1/accounts/balances", handlers.CustomHandler(accountBalancesHandler.GetAccountBalances), true},
		{http.MethodPost, "/api/v1/token-prices", handlers.CustomHandler(tokenPricesHandler.GetPrices), true},
		{http.MethodGet, "/api/v1/accounts/{address}/transactions", handlers.CustomHandler(accountHistoryHandler.GetAccountTransactions), true},
		{http.MethodGet, "/api/v1/accounts/{address}/positions", handlers.CustomHandler(accountPositionsHandler.GetAccountPositions), true},
		{http.MethodGet, "/api/v1/auth/whoami", handlers.CustomHandler(whoamiHandler.Whoami), true},
	}, nil
}

func (s *ApiServer) initHandlers() (*http.ServeMux, error) {
	rts, err := s.routes()
	if err != nil {
		return nil, err
	}

	// One Auth instance, bound to s.authMode (resolved once in Start), wraps every
	// gated route. A future user-scoped route opts into auth simply by adding itself
	// to routes() with gated=true.
	verifier := auth.NewVerifier()
	authed := middleware.Auth(verifier, s.authMode, s.appMetrics.Auth)

	mux := http.NewServeMux()
	for _, rt := range rts {
		h := rt.handler
		if rt.gated {
			h = authed(h)
		}
		mux.Handle(rt.method+" "+rt.pattern, h)
	}

	return mux, nil
}

func (s *ApiServer) initMetricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{Registry: s.registry}))
	return mux
}

func (s *ApiServer) initMiddleware(mux *http.ServeMux) http.Handler {
	middlewares := []middleware.Middleware{
		middleware.Recover(),
		middleware.ResponseHeader(),
		middleware.BodySizeLimit(s.cfg.AppConfig.MaxRequestBodySize),
		middleware.Logging(),
		middleware.Metrics(s.appMetrics.HTTP),
	}

	// Apply the middlewares to the mux
	handler := middleware.Chain(mux, middlewares...)
	return handler
}

func (s *ApiServer) startServers(apiHandler http.Handler, metricsHandler http.Handler) error {
	apiServer := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", s.cfg.AppConfig.FreighterBackendHost, s.cfg.AppConfig.FreighterBackendPort),
		Handler:      apiHandler,
		ReadTimeout:  DefaultReadTimeout,
		WriteTimeout: DefaultWriteTimeout,
		IdleTimeout:  DefaultIdleTimeout,
	}
	metricsServer := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", s.cfg.AppConfig.MetricsHost, s.cfg.AppConfig.MetricsPort),
		Handler:      metricsHandler,
		ReadTimeout:  DefaultReadTimeout,
		WriteTimeout: DefaultWriteTimeout,
		IdleTimeout:  DefaultIdleTimeout,
	}

	// errgroup: if either ListenAndServe returns an unexpected error, ctx is
	// canceled so we tear down the surviving server and surface the failure.
	g, ctx := errgroup.WithContext(context.Background())
	g.Go(func() error {
		logger.Info("Starting API server", "address", apiServer.Addr)
		if err := apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("api server: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		logger.Info("Starting metrics server", "address", metricsServer.Addr)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("metrics server: %w", err)
		}
		return nil
	})

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		logger.Info("Shutting down servers...")
	case <-ctx.Done():
		logger.Error("Server exited unexpectedly; shutting down siblings")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), ServerShutdownTimeout)
	defer cancel()

	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("API server forced to shutdown", "error", err)
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("Metrics server forced to shutdown", "error", err)
	}

	if err := g.Wait(); err != nil {
		return err
	}
	logger.Info("Servers gracefully stopped")
	return nil
}
