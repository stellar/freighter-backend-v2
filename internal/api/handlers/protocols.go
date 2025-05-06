package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/logger"
)

// ProtocolHandler holds dependencies for protocol-related handlers.
type ProtocolsHandler struct {
	protocolsConfigPath string
}

// NewProtocolHandler creates a new ProtocolHandler instance.
func NewProtocolsHandler(protocolsConfigPath string) *ProtocolsHandler {
	return &ProtocolsHandler{
		protocolsConfigPath: protocolsConfigPath,
	}
}

type Protocol struct {
	Name          string   `json:"name"`
	Tags          []string `json:"tags"`
	URL           string   `json:"website_url"`
	IconURL       string   `json:"icon_url"`
	Description   string   `json:"description"`
	IsBlacklisted bool     `json:"is_blacklisted"`
}

type GetProtocolsResponse struct {
	Data []Protocol `json:"data"`
}

// GetProtocols handles requests to fetch the list of supported protocols.
// It reads the protocol information based on the configured path.
func (h *ProtocolsHandler) GetProtocols(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	data, err := os.ReadFile(h.protocolsConfigPath)
	if err != nil {
		errStr := fmt.Sprintf("failed to read protocols config: %v", err)
		logger.ErrorWithContext(ctx, errStr)
		return httperror.NewHttpError(errStr, err, http.StatusInternalServerError, nil)
	}

	var protocols []Protocol
	err = json.Unmarshal(data, &protocols)
	if err != nil {
		errStr := fmt.Sprintf("failed to unmarshal protocols config: %v", err)
		logger.ErrorWithContext(ctx, errStr)
		return httperror.NewHttpError(errStr, err, http.StatusInternalServerError, nil)
	}

	response := GetProtocolsResponse{
		Data: protocols,
	}

	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		errStr := fmt.Sprintf("failed to encode protocols to JSON response: %v", err)
		logger.ErrorWithContext(ctx, errStr)
		return httperror.NewHttpError(errStr, err, http.StatusInternalServerError, nil)
	}
	return nil
}
