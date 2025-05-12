package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/logger"
)

var (
	ErrFailedToReadProtocolsConfig = httperror.ErrorMessage{
		LogMessage:    "failed to read protocols config from %s: %v",
		ClientMessage: "An error occurred while fetching protocol configurations.",
	}
	ErrFailedToUnmarshalProtocolsConfig = httperror.ErrorMessage{
		LogMessage:    "failed to unmarshal protocols config: %v. Data (first %d bytes): %s",
		ClientMessage: "An error occurred while processing protocol configurations.",
	}
	ErrFailedToEncodeProtocolsToJSONResponse = httperror.ErrorMessage{
		LogMessage:    "failed to encode protocols to JSON response: %v",
		ClientMessage: "An error occurred while formatting the response.",
	}
)

type Protocol struct {
	Name          string   `json:"name"`
	Tags          []string `json:"tags"`
	URL           string   `json:"website_url"`
	IconURL       string   `json:"icon_url"`
	Description   string   `json:"description"`
	IsBlacklisted bool     `json:"is_blacklisted"`
}

// ProtocolsPayload encapsulates the list of protocols under a specific key.
// This is used to structure the response under a "data" field.
type GetProtocolsPayload struct {
	Protocols []Protocol `json:"protocols"`
}

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

// GetProtocols handles requests to fetch the list of supported protocols.
// It reads the protocol information based on the configured path.
func (h *ProtocolsHandler) GetProtocols(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	data, err := os.ReadFile(h.protocolsConfigPath)
	if err != nil {
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrFailedToReadProtocolsConfig.LogMessage, h.protocolsConfigPath, err))
		return httperror.NewHttpError(ErrFailedToReadProtocolsConfig.ClientMessage, err, http.StatusInternalServerError, nil)
	}

	var protocols []Protocol
	err = json.Unmarshal(data, &protocols)
	if err != nil {
		// Helper function to safely get a snippet of data for logging
		snippetLength := 100
		if len(data) < snippetLength {
			snippetLength = len(data)
		}
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrFailedToUnmarshalProtocolsConfig.LogMessage, err, snippetLength, string(data[:snippetLength])))
		return httperror.NewHttpError(ErrFailedToUnmarshalProtocolsConfig.ClientMessage, err, http.StatusInternalServerError, nil)
	}

	response := HttpResponse{
		Data: GetProtocolsPayload{
			Protocols: protocols,
		},
	}

	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrFailedToEncodeProtocolsToJSONResponse.LogMessage, err))
		return httperror.NewHttpError(ErrFailedToEncodeProtocolsToJSONResponse.ClientMessage, err, http.StatusInternalServerError, nil)
	}
	return nil
}
