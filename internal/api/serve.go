package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
	"github.com/stellar/freighter-backend-v2/internal/api/middleware"
	"github.com/stellar/freighter-backend-v2/internal/config"
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
)

type ApiServer struct {
	cfg                  *config.Config
	redis                *store.RedisStore
	rpcService           types.RPCService
	walletBackendService types.WalletBackendService
	pricesService        types.PricesService
	registry             *prometheus.Registry
	appMetrics           *metrics.Metrics
}

func NewApiServer(cfg *config.Config) *ApiServer {
	return &ApiServer{cfg: cfg}
}

func (s *ApiServer) Start() error {
	s.registry = prometheus.NewRegistry()
	s.registry.MustRegister(collectors.NewGoCollector())
	s.registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	s.registry.MustRegister(collectors.NewBuildInfoCollector())
	s.appMetrics = metrics.NewMetrics(s.registry)

	if err := s.initServices(); err != nil {
		logger.Error("Failed to initialize services", "error", err)
		return err
	}

	apiHandler := s.initMiddleware(s.initHandlers())
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

func (s *ApiServer) initHandlers() *http.ServeMux {
	mux := http.NewServeMux()

	// Initialize health check handler
	healthHandler := handlers.NewHealthHandler()
	mux.HandleFunc("GET /api/v1/ping", handlers.CustomHandler(healthHandler.CheckHealth))

	// Initialize RPC health check handler
	rpcHealthHandler := handlers.NewRPCHealthHandler(s.rpcService)
	mux.HandleFunc("GET /api/v1/rpc-health", handlers.CustomHandler(rpcHealthHandler.CheckRPCHealth))

	protocolsHandler := handlers.NewProtocolsHandler(s.cfg.AppConfig.ProtocolsConfigPath)
	mux.HandleFunc("GET /api/v1/protocols", handlers.CustomHandler(protocolsHandler.GetProtocols))

	collectiblesHandler := handlers.NewCollectiblesHandler(s.rpcService, s.cfg.AppConfig.MeridianPayTreasureHuntAddress, s.cfg.AppConfig.MeridianPayTreasurePoapAddress, s.cfg.AppConfig.MeridianPayStellarHouseAddress, s.cfg.RpcConfig.MaxConcurrentRPCCalls)
	mux.HandleFunc("POST /api/v1/collectibles", handlers.CustomHandler(collectiblesHandler.GetCollectibles))

	ledgerKeyAccountsHandler := handlers.NewLedgerKeyAccountHandler(s.rpcService, s.cfg.AppConfig.MaxLedgerKeyAddresses)
	mux.HandleFunc("POST /api/v1/ledger-key/accounts", handlers.CustomHandler(ledgerKeyAccountsHandler.GetLedgerKeyAccounts))

	featureFlagsHandler := handlers.NewFeatureFlagsHandler()
	mux.HandleFunc("GET /api/v1/feature-flags", handlers.CustomHandler(featureFlagsHandler.GetFeatureFlags))

	accountBalancesHandler := handlers.NewAccountBalancesHandler(s.walletBackendService, s.cfg.AppConfig.MaxBalanceAddresses)
	mux.HandleFunc("POST /api/v1/accounts/balances", handlers.CustomHandler(accountBalancesHandler.GetAccountBalances))

	tokenPricesHandler := handlers.NewTokenPricesHandler(s.pricesService, s.cfg.PricesConfig.MaxTokensPerRequest)
	mux.HandleFunc("POST /api/v1/token-prices", handlers.CustomHandler(tokenPricesHandler.GetPrices))
	return mux
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
