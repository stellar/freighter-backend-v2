package handlers

import (
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/logger"
)

func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte("OK"))
	if err != nil {
		logger.Error("Error writing health check response", "error", err)
	}
}
