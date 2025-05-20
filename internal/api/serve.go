package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
	"github.com/stellar/freighter-backend-v2/internal/api/middleware"
	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/interfaces"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/services"
	"github.com/stellar/freighter-backend-v2/internal/store"
)

const (
	DefaultReadTimeout    = 10 * time.Second
	DefaultWriteTimeout   = 10 * time.Second
	DefaultIdleTimeout    = 120 * time.Second
	ServerShutdownTimeout = 10 * time.Second
)

type ApiServer struct {
	cfg        *config.Config
	redis      *store.RedisStore
	rpcService interfaces.RPCService
}

func NewApiServer(cfg *config.Config) *ApiServer {
	return &ApiServer{cfg: cfg}
}

func (s *ApiServer) Start() error {
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
	redisConn, err := store.NewRedisStore(s.cfg.RedisConfig.Host, s.cfg.RedisConfig.Port, s.cfg.RedisConfig.Password)
	if err != nil {
		logger.Error("Failed to initialize Redis store", "error", err)
		return err
	}
	s.redis = redisConn

	rpcService := services.NewRPCService(s.cfg.RpcConfig.RpcUrl)
	s.rpcService = rpcService
	return nil
}

func (s *ApiServer) initHandlers() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/ping", handlers.CustomHandler(handlers.HealthCheckHandler))

	protocolsHandler := handlers.NewProtocolsHandler(s.cfg.AppConfig.ProtocolsConfigPath)
	mux.HandleFunc("GET /api/v1/protocols", handlers.CustomHandler(protocolsHandler.GetProtocols))
	return mux
}

func (s *ApiServer) initMiddleware(mux *http.ServeMux) http.Handler {
	middlewares := []middleware.Middleware{
		middleware.Recover(),
		middleware.ResponseHeader(),
		middleware.Logging(),
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
