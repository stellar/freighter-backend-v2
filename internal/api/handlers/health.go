package handlers

import (
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/logger"
)

func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte("OK"))
	if err != nil {
		logger.Error("Error writing response: %v", err)
	}
}
