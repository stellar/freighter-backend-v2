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

	mux := s.initHandlers()
	handler := s.initMiddleware(mux)
	s.startServer(handler)
	return nil
}

func (s *ApiServer) initServices() error {
	s.redis = store.NewRedisStore(s.cfg.RedisConfig.Host, s.cfg.RedisConfig.Port, s.cfg.RedisConfig.Password)

	s.rpcService = services.NewRPCService(s.cfg.RpcConfig.PubnetRpcUrl, s.cfg.RpcConfig.TestnetRpcUrl, s.cfg.RpcConfig.FuturenetRpcUrl, s.appMetrics.Service)

	// Initialize wallet backend service if configured
	walletBackendService, err := services.NewWalletBackendService(
		s.cfg.WalletBackendConfig.PubnetUrl,
		s.cfg.WalletBackendConfig.TestnetUrl,
		s.cfg.WalletBackendConfig.PubnetSigningKey,
		s.cfg.WalletBackendConfig.TestnetSigningKey,
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
		s.appMetrics.Service,
	)
	s.pricesService = services.NewPricesService(stellarExpert, s.redis, services.PricesServiceConfig{
		CacheTTL:      time.Duration(s.cfg.PricesConfig.PriceCacheTTLSeconds) * time.Second,
		MaxConcurrent: s.cfg.PricesConfig.MaxConcurrentPriceFetches,
	}, s.appMetrics.Service)

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
	mux.HandleFunc("POST /api/v1/account-balances", handlers.CustomHandler(accountBalancesHandler.GetAccountBalances))

	if !s.cfg.PricesConfig.DisableTokenPrices {
		tokenPricesHandler := handlers.NewTokenPricesHandler(s.pricesService, s.cfg.PricesConfig.MaxTokensPerRequest)
		mux.HandleFunc("POST /api/v1/token-prices", handlers.CustomHandler(tokenPricesHandler.GetPrices))
	}

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

func (s *ApiServer) startServer(handler http.Handler) {
	server := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", s.cfg.AppConfig.FreighterBackendHost, s.cfg.AppConfig.FreighterBackendPort),
		Handler:      handler,
		ReadTimeout:  DefaultReadTimeout,
		WriteTimeout: DefaultWriteTimeout,
		IdleTimeout:  DefaultIdleTimeout,
	}

	// Start the server in a goroutine
	go func() {
		logger.Info("Starting HTTP server", "address", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
		}
	}()

	// Wait for termination signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// Graceful shutdown
	logger.Info("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), ServerShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
	}

	logger.Info("Server gracefully stopped")
}
