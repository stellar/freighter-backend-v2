package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

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

// GetProtocols handles requests to fetch the list of supported protocols.
// It reads the protocol information based on the configured path.
func (h *ProtocolsHandler) GetProtocols(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(h.protocolsConfigPath)
	if err != nil {
		errString := fmt.Sprintf("Failed to read protocols config: %v", err)
		logger.Error(errString)
		http.Error(w, errString, http.StatusInternalServerError)
		return
	}

	var protocols []Protocol
	err = json.Unmarshal(data, &protocols)
	if err != nil {
		errString := fmt.Sprintf("Failed to unmarshal protocols config: %v", err)
		logger.Error(errString)
		http.Error(w, errString, http.StatusInternalServerError)
		return
	}

	err = json.NewEncoder(w).Encode(protocols)
	if err != nil {
		errString := fmt.Sprintf("Failed to encode protocols to JSON response: %v", err)
		logger.Error(errString)
		http.Error(w, errString, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
}
