package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/logger"
)

func HealthCheckHandler(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	_, err := w.Write([]byte("OK"))
	if err != nil {
		errStr := fmt.Sprintf("writing health check response: %v", err)
		logger.ErrorWithContext(ctx, errStr)
		return WithHttpStatus(errors.New(errStr), http.StatusInternalServerError)
	}
	return nil
}
