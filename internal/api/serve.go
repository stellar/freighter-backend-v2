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
	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/store"
)

type Service interface {
	Ping(ctx context.Context) error
}

type ApiServer struct {
	cfg *config.Config
	srv *http.Server
}

func NewApiServer(cfg *config.Config) *ApiServer {
	return &ApiServer{cfg: cfg}
}

func (s *ApiServer) Start() error {
	if err := s.initServices(); err != nil {
		logger.Error("Failed to initialize services", "error", err)
		return err
	}

	mux := s.setupHandlers()
	s.startServer(mux)
	return nil
}

func (s *ApiServer) initServices() error {
	_, err := store.NewRedisStore(s.cfg)
	if err != nil {
		logger.Error("Failed to initialize Redis store", "error", err)
		return err
	}

	return nil
}

func (s *ApiServer) setupHandlers() *http.ServeMux {
	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("GET /api/v1/ping", handlers.HealthCheckHandler)
	return mux
}

func (s *ApiServer) startServer(mux *http.ServeMux) {
	s.srv = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", s.cfg.AppConfig.FreighterBackendHost, s.cfg.AppConfig.FreighterBackendPort),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start the server in a goroutine
	go func() {
		logger.Info("Starting HTTP server", "address", s.srv.Addr)
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
		}
	}()

	// Wait for termination signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// Graceful shutdown
	logger.Info("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.srv.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
	}

	logger.Info("Server gracefully stopped")
}
